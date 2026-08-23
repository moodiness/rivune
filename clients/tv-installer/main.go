package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/moodiness/rivune/clients/tv-installer/internal/install"
	"github.com/moodiness/rivune/clients/tv-installer/internal/release"
	installerweb "github.com/moodiness/rivune/clients/tv-installer/internal/web"
)

var version = "dev"

func main() {
	noOpen := flag.Bool("no-open", false, "print the local installer URL without opening a browser")
	showVersion := flag.Bool("version", false, "print the companion version")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}

	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		fatal(err)
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		fatal(err)
	}
	token := hex.EncodeToString(tokenBytes)
	shutdown := make(chan struct{}, 1)
	service := &install.Service{Source: release.NewClient()}
	handler := installerweb.New(service, version, token, listener.Addr().String(), func() {
		select {
		case shutdown <- struct{}{}:
		default:
		}
	})
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			fatal(err)
		}
	}()
	url := "http://" + listener.Addr().String() + "/" + token + "/"
	fmt.Println("Rivune TV Installer:", url)
	if !*noOpen {
		if err := openBrowser(url); err != nil {
			fmt.Fprintln(os.Stderr, "Open this URL in a browser:", url)
		}
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	select {
	case <-shutdown:
	case <-signals:
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}

func openBrowser(url string) error {
	var command string
	var arguments []string
	switch runtime.GOOS {
	case "darwin":
		command, arguments = "open", []string{url}
	case "windows":
		command, arguments = "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default:
		command, arguments = "xdg-open", []string{url}
	}
	return exec.Command(command, arguments...).Start()
}

func fatal(err error) { fmt.Fprintln(os.Stderr, "Rivune TV Installer:", err); os.Exit(1) }
