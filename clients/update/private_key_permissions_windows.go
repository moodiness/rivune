//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

const privateKeySensitiveAccess = windows.FILE_READ_DATA |
	windows.FILE_WRITE_DATA |
	windows.FILE_APPEND_DATA |
	windows.FILE_EXECUTE |
	windows.DELETE |
	windows.WRITE_DAC |
	windows.WRITE_OWNER |
	windows.GENERIC_READ |
	windows.GENERIC_WRITE |
	windows.GENERIC_EXECUTE |
	windows.GENERIC_ALL |
	windows.MAXIMUM_ALLOWED

func validatePrivateKeyPermissions(file *os.File) error {
	descriptor, err := windows.GetSecurityInfo(
		windows.Handle(file.Fd()),
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return fmt.Errorf("inspect private key permissions: %w", err)
	}
	if descriptor == nil {
		return errors.New("private key permissions could not be verified")
	}

	tokenUser, err := windows.GetCurrentThreadEffectiveToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("inspect private key owner: %w", err)
	}
	systemSID, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return fmt.Errorf("identify trusted private key owner: %w", err)
	}
	administratorsSID, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return fmt.Errorf("identify trusted private key owner: %w", err)
	}

	err = validatePrivateKeySecurityDescriptor(descriptor, []*windows.SID{
		tokenUser.User.Sid,
		systemSID,
		administratorsSID,
	})
	runtime.KeepAlive(tokenUser)
	return err
}

func validatePrivateKeySecurityDescriptor(descriptor *windows.SECURITY_DESCRIPTOR, trustedSIDs []*windows.SID) error {
	if descriptor == nil || !descriptor.IsValid() {
		return errors.New("private key permissions could not be verified")
	}
	for _, sid := range trustedSIDs {
		if sid == nil || !sid.IsValid() {
			return errors.New("private key permissions could not be verified")
		}
	}

	owner, _, err := descriptor.Owner()
	if err != nil || owner == nil || !owner.IsValid() {
		return errors.New("private key owner could not be verified")
	}
	if !privateKeySIDIsTrusted(owner, trustedSIDs) {
		return errors.New("private key owner is not trusted")
	}

	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		return errors.New("private key permissions must include a restrictive DACL")
	}
	descriptorStart := uintptr(unsafe.Pointer(descriptor))
	descriptorLength := uintptr(descriptor.Length())
	descriptorEnd := descriptorStart + descriptorLength
	daclStart := uintptr(unsafe.Pointer(dacl))
	if descriptorEnd < descriptorStart ||
		daclStart < descriptorStart ||
		daclStart > descriptorEnd ||
		descriptorEnd-daclStart < unsafe.Sizeof(windows.ACL{}) {
		return errors.New("private key permissions contain an invalid DACL")
	}
	for index := uint16(0); index < dacl.AceCount; index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(index), &ace); err != nil || ace == nil {
			return errors.New("private key permissions could not be verified")
		}
		aceStart := uintptr(unsafe.Pointer(ace))
		if aceStart < descriptorStart ||
			aceStart > descriptorEnd ||
			descriptorEnd-aceStart < unsafe.Sizeof(windows.ACE_HEADER{}) {
			return errors.New("private key permissions contain an invalid access rule")
		}
		aceBytes := uintptr(ace.Header.AceSize)
		if aceBytes < unsafe.Sizeof(windows.ACE_HEADER{}) || aceBytes > descriptorEnd-aceStart {
			return errors.New("private key permissions contain an invalid access rule")
		}
		if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 {
			continue
		}

		switch ace.Header.AceType {
		case windows.ACCESS_DENIED_ACE_TYPE:
			continue
		case windows.ACCESS_ALLOWED_ACE_TYPE:
			if err := validatePrivateKeyAllowedACE(ace, int(aceBytes), trustedSIDs); err != nil {
				return err
			}
		default:
			return errors.New("private key permissions contain an unsupported access rule")
		}
	}
	return nil
}

func validatePrivateKeyAllowedACE(ace *windows.ACCESS_ALLOWED_ACE, aceBytes int, trustedSIDs []*windows.SID) error {
	const sidHeaderBytes = 8
	sidOffset := int(unsafe.Offsetof(windows.ACCESS_ALLOWED_ACE{}.SidStart))
	if aceBytes%4 != 0 || aceBytes < sidOffset+sidHeaderBytes {
		return errors.New("private key permissions contain an invalid access rule")
	}

	sidBytes := unsafe.Slice((*byte)(unsafe.Pointer(&ace.SidStart)), aceBytes-sidOffset)
	sidLength := sidHeaderBytes + int(sidBytes[1])*4
	if sidBytes[0] != 1 || sidLength != len(sidBytes) {
		return errors.New("private key permissions contain an invalid security identifier")
	}
	sid := (*windows.SID)(unsafe.Pointer(&sidBytes[0]))
	if !sid.IsValid() {
		return errors.New("private key permissions contain an invalid security identifier")
	}
	if ace.Mask&privateKeySensitiveAccess != 0 && !privateKeySIDIsTrusted(sid, trustedSIDs) {
		return errors.New("private key permissions grant sensitive access to an untrusted account")
	}
	return nil
}

func privateKeySIDIsTrusted(sid *windows.SID, trustedSIDs []*windows.SID) bool {
	for _, trustedSID := range trustedSIDs {
		if sid.Equals(trustedSID) {
			return true
		}
	}
	return false
}
