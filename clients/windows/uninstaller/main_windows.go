package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const (
	messageBoxYesNo       = uintptr(0x00000004)
	messageBoxQuestion    = uintptr(0x00000020)
	messageBoxError       = uintptr(0x00000010)
	messageBoxInformation = uintptr(0x00000040)
	messageBoxYes         = uintptr(6)
	synchronize           = uintptr(0x00100000)
	infinite              = uintptr(0xffffffff)
	moveFileDelayReboot   = uintptr(0x00000004)
	hkeyCurrentUser       = uintptr(0x80000001)
)

func main() {
	arguments := os.Args[1:]
	quiet := len(arguments) == 1 && arguments[0] == "--quiet" ||
		len(arguments) == 4 && arguments[0] == "--complete" && arguments[3] == "true"
	if err := run(arguments); err != nil {
		if !quiet {
			messageBox("Could not uninstall Rivune", err.Error(), messageBoxError)
		}
		os.Exit(1)
	}
}

func run(arguments []string) error {
	installDirectory, err := expectedInstallDirectory()
	if err != nil {
		return err
	}
	if len(arguments) >= 1 && arguments[0] == "--complete" {
		if len(arguments) != 4 || !samePath(arguments[1], installDirectory) {
			return errors.New("the uninstall completion request is invalid")
		}
		parentPID, err := strconv.ParseUint(arguments[2], 10, 32)
		if err != nil || parentPID == 0 {
			return errors.New("the uninstall process identifier is invalid")
		}
		quiet, err := strconv.ParseBool(arguments[3])
		if err != nil {
			return errors.New("the uninstall completion mode is invalid")
		}
		waitForProcess(uint32(parentPID))
		return complete(installDirectory, quiet)
	}
	quiet := len(arguments) == 1 && arguments[0] == "--quiet"
	if len(arguments) > 1 || (len(arguments) == 1 && !quiet) {
		return errors.New("the uninstall command line is invalid")
	}
	if !quiet && messageBox(
		"Uninstall Rivune",
		"Remove Rivune for this Windows user? Your Rivune settings and sessions in AppData will be kept.",
		messageBoxYesNo|messageBoxQuestion) != messageBoxYes {
		return nil
	}

	self, err := os.Executable()
	if err != nil {
		return err
	}
	if !samePath(filepath.Dir(self), installDirectory) {
		return errors.New("the uninstaller is not running from the Rivune installation folder")
	}
	temporary, err := os.CreateTemp("", "rivune-uninstall-*.exe")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := copyFile(self, temporaryPath); err != nil {
		_ = os.Remove(temporaryPath)
		return err
	}
	command := exec.Command(temporaryPath, "--complete", installDirectory, strconv.Itoa(os.Getpid()), strconv.FormatBool(quiet))
	if err := command.Start(); err != nil {
		_ = os.Remove(temporaryPath)
		return err
	}
	return nil
}

func complete(installDirectory string, quiet bool) error {
	startMenu := filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs", "Rivune.lnk")
	_ = os.Remove(startMenu)
	if _, markerErr := os.Stat(filepath.Join(installDirectory, ".rivune-desktop-shortcut")); markerErr == nil {
		if desktop, err := desktopDirectory(); err == nil {
			_ = os.Remove(filepath.Join(desktop, "Rivune.lnk"))
		}
	}
	var lastError error
	for range 20 {
		if err := os.RemoveAll(installDirectory); err == nil {
			lastError = nil
			break
		} else {
			lastError = err
			time.Sleep(250 * time.Millisecond)
		}
	}
	if lastError != nil {
		return fmt.Errorf("close Rivune and try again: %w", lastError)
	}
	if err := deleteUninstallRegistry(); err != nil {
		return err
	}
	self, _ := os.Executable()
	if self != "" {
		moveFileEx(self, moveFileDelayReboot)
	}
	if !quiet {
		messageBox("Rivune uninstalled", "Rivune was removed. Your settings and sessions in AppData were kept.", messageBoxInformation)
	}
	return nil
}

func expectedInstallDirectory() (string, error) {
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		return "", errors.New("Windows did not report the local application data folder")
	}
	return filepath.Clean(filepath.Join(local, "Programs", "Rivune")), nil
}

func copyFile(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_TRUNC, 0o700)
	if err != nil {
		return err
	}
	if _, err := output.ReadFrom(input); err != nil {
		output.Close()
		return err
	}
	return output.Close()
}

func waitForProcess(pid uint32) {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	openProcess := kernel32.NewProc("OpenProcess")
	waitForSingleObject := kernel32.NewProc("WaitForSingleObject")
	closeHandle := kernel32.NewProc("CloseHandle")
	handle, _, _ := openProcess.Call(synchronize, 0, uintptr(pid))
	if handle == 0 {
		return
	}
	waitForSingleObject.Call(handle, infinite)
	closeHandle.Call(handle)
}

func deleteUninstallRegistry() error {
	advapi32 := syscall.NewLazyDLL("advapi32.dll")
	regDeleteTree := advapi32.NewProc("RegDeleteTreeW")
	key, _ := syscall.UTF16PtrFromString(`Software\Microsoft\Windows\CurrentVersion\Uninstall\Rivune`)
	result, _, _ := regDeleteTree.Call(hkeyCurrentUser, uintptr(unsafe.Pointer(key)))
	if result != 0 && result != 2 {
		return fmt.Errorf("Windows could not remove the Rivune uninstall registration (error %d)", result)
	}
	return nil
}

func desktopDirectory() (string, error) {
	user32 := syscall.NewLazyDLL("shell32.dll")
	shGetFolderPath := user32.NewProc("SHGetFolderPathW")
	buffer := make([]uint16, syscall.MAX_PATH)
	result, _, _ := shGetFolderPath.Call(0, uintptr(0x0010), 0, 0, uintptr(unsafe.Pointer(&buffer[0])))
	if result != 0 {
		return "", fmt.Errorf("Windows could not locate the desktop folder (error %d)", result)
	}
	return syscall.UTF16ToString(buffer), nil
}

func moveFileEx(path string, flags uintptr) {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	procedure := kernel32.NewProc("MoveFileExW")
	value, _ := syscall.UTF16PtrFromString(path)
	procedure.Call(uintptr(unsafe.Pointer(value)), 0, flags)
}

func messageBox(title, text string, flags uintptr) uintptr {
	user32 := syscall.NewLazyDLL("user32.dll")
	procedure := user32.NewProc("MessageBoxW")
	titleValue, _ := syscall.UTF16PtrFromString(title)
	textValue, _ := syscall.UTF16PtrFromString(text)
	result, _, _ := procedure.Call(0, uintptr(unsafe.Pointer(textValue)), uintptr(unsafe.Pointer(titleValue)), flags)
	return result
}

func samePath(left, right string) bool {
	leftPath, leftErr := filepath.Abs(left)
	rightPath, rightErr := filepath.Abs(right)
	return leftErr == nil && rightErr == nil && strings.EqualFold(filepath.Clean(leftPath), filepath.Clean(rightPath))
}
