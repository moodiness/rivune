//go:build windows

package main

import (
	"runtime"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestReadP256PrivateKeyAcceptsPrivateTemporaryFile(t *testing.T) {
	path := t.TempDir() + `\private.pem`
	want, _ := writeTestKey(t, path)

	got, err := readP256PrivateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(want) {
		t.Fatal("private key changed during round trip")
	}
}

func TestReadP256PrivateKeyRejectsEveryoneRead(t *testing.T) {
	path := t.TempDir() + `\private.pem`
	writeTestKey(t, path)

	user := currentTestUserSID(t)
	everyone := wellKnownTestSID(t, windows.WinWorldSid)
	acl := testACL(t,
		testAccess(user, windows.GENERIC_ALL, windows.TRUSTEE_IS_USER),
		testAccess(everyone, windows.FILE_GENERIC_READ, windows.TRUSTEE_IS_GROUP),
	)
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}

	_, err := readP256PrivateKey(path)
	if err == nil {
		t.Fatal("private key with Everyone read access was accepted")
	}
	if !strings.Contains(err.Error(), "private key permissions") {
		t.Fatalf("error is not actionable: %v", err)
	}
}

func TestValidatePrivateKeySecurityDescriptorRejectsUnsafeDescriptors(t *testing.T) {
	user := currentTestUserSID(t)
	everyone := wellKnownTestSID(t, windows.WinWorldSid)
	trusted := trustedTestSIDs(t, user)

	t.Run("missing DACL", func(t *testing.T) {
		sd := testSecurityDescriptor(t, user, nil, false)
		if err := validatePrivateKeySecurityDescriptor(sd, trusted); err == nil {
			t.Fatal("security descriptor without a DACL was accepted")
		}
	})

	t.Run("NULL DACL", func(t *testing.T) {
		sd := testSecurityDescriptor(t, user, nil, true)
		if err := validatePrivateKeySecurityDescriptor(sd, trusted); err == nil {
			t.Fatal("security descriptor with a NULL DACL was accepted")
		}
	})

	t.Run("unrelated owner", func(t *testing.T) {
		acl := testACL(t, testAccess(user, windows.GENERIC_ALL, windows.TRUSTEE_IS_USER))
		sd := testSecurityDescriptor(t, everyone, acl, true)
		if err := validatePrivateKeySecurityDescriptor(sd, trusted); err == nil {
			t.Fatal("security descriptor with an unrelated owner was accepted")
		}
	})

	t.Run("unsupported ACE", func(t *testing.T) {
		acl := testACL(t, testAccess(user, windows.GENERIC_ALL, windows.TRUSTEE_IS_USER))
		ace := testACE(t, acl)
		ace.Header.AceType = 5 // ACCESS_ALLOWED_OBJECT_ACE_TYPE has a different layout.
		sd := testSecurityDescriptor(t, user, acl, true)
		if err := validatePrivateKeySecurityDescriptor(sd, trusted); err == nil {
			t.Fatal("unsupported ACE type was accepted")
		}
	})

	t.Run("invalid SID", func(t *testing.T) {
		acl := testACL(t, testAccess(everyone, windows.FILE_GENERIC_READ, windows.TRUSTEE_IS_GROUP))
		ace := testACE(t, acl)
		sidBytes := unsafe.Slice((*byte)(unsafe.Pointer(&ace.SidStart)), int(ace.Header.AceSize)-int(unsafe.Offsetof(ace.SidStart)))
		sidBytes[0] = 0 // SID revisions other than one are invalid.
		sd := testSecurityDescriptor(t, user, acl, true)
		if err := validatePrivateKeySecurityDescriptor(sd, trusted); err == nil {
			t.Fatal("ACE with an invalid SID was accepted")
		}
	})
}

func TestValidatePrivateKeySecurityDescriptorACEPolicy(t *testing.T) {
	user := currentTestUserSID(t)
	everyone := wellKnownTestSID(t, windows.WinWorldSid)
	trusted := trustedTestSIDs(t, user)

	for name, mask := range map[string]windows.ACCESS_MASK{
		"read":            windows.FILE_READ_DATA,
		"write":           windows.FILE_WRITE_DATA,
		"append":          windows.FILE_APPEND_DATA,
		"execute":         windows.FILE_EXECUTE,
		"delete":          windows.DELETE,
		"change DACL":     windows.WRITE_DAC,
		"change owner":    windows.WRITE_OWNER,
		"generic read":    windows.GENERIC_READ,
		"generic write":   windows.GENERIC_WRITE,
		"generic execute": windows.GENERIC_EXECUTE,
		"generic all":     windows.GENERIC_ALL,
		"maximum allowed": windows.MAXIMUM_ALLOWED,
	} {
		t.Run(name, func(t *testing.T) {
			acl := testACL(t, testAccess(everyone, mask, windows.TRUSTEE_IS_GROUP))
			sd := testSecurityDescriptor(t, user, acl, true)
			if err := validatePrivateKeySecurityDescriptor(sd, trusted); err == nil {
				t.Fatalf("untrusted sensitive allow mask %#x was accepted", uint32(mask))
			}
		})
	}

	t.Run("inherited allow counts", func(t *testing.T) {
		acl := testACL(t, testAccess(everyone, windows.FILE_GENERIC_READ, windows.TRUSTEE_IS_GROUP))
		testACE(t, acl).Header.AceFlags |= windows.INHERITED_ACE
		sd := testSecurityDescriptor(t, user, acl, true)
		if err := validatePrivateKeySecurityDescriptor(sd, trusted); err == nil {
			t.Fatal("inherited Everyone read ACE was accepted")
		}
	})

	t.Run("inherit only is skipped", func(t *testing.T) {
		acl := testACL(t, testAccess(everyone, windows.FILE_GENERIC_READ, windows.TRUSTEE_IS_GROUP))
		testACE(t, acl).Header.AceFlags |= windows.INHERIT_ONLY_ACE
		sd := testSecurityDescriptor(t, user, acl, true)
		if err := validatePrivateKeySecurityDescriptor(sd, trusted); err != nil {
			t.Fatalf("inherit-only ACE applies only to children: %v", err)
		}
	})

	t.Run("deny is skipped", func(t *testing.T) {
		acl := testACL(t, testAccess(everyone, windows.FILE_GENERIC_READ, windows.TRUSTEE_IS_GROUP))
		testACE(t, acl).Header.AceType = windows.ACCESS_DENIED_ACE_TYPE
		sd := testSecurityDescriptor(t, user, acl, true)
		if err := validatePrivateKeySecurityDescriptor(sd, trusted); err != nil {
			t.Fatalf("deny ACE cannot broaden access: %v", err)
		}
	})
}

func currentTestUserSID(t *testing.T) *windows.SID {
	t.Helper()
	user, err := windows.GetCurrentThreadEffectiveToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sid, err := user.User.Sid.Copy()
	if err != nil {
		t.Fatal(err)
	}
	return sid
}

func wellKnownTestSID(t *testing.T, sidType windows.WELL_KNOWN_SID_TYPE) *windows.SID {
	t.Helper()
	sid, err := windows.CreateWellKnownSid(sidType)
	if err != nil {
		t.Fatal(err)
	}
	return sid
}

func trustedTestSIDs(t *testing.T, user *windows.SID) []*windows.SID {
	t.Helper()
	return []*windows.SID{
		user,
		wellKnownTestSID(t, windows.WinLocalSystemSid),
		wellKnownTestSID(t, windows.WinBuiltinAdministratorsSid),
	}
}

type testAccessEntry struct {
	sid         *windows.SID
	permissions windows.ACCESS_MASK
	trusteeType windows.TRUSTEE_TYPE
}

func testAccess(sid *windows.SID, permissions windows.ACCESS_MASK, trusteeType windows.TRUSTEE_TYPE) testAccessEntry {
	return testAccessEntry{sid: sid, permissions: permissions, trusteeType: trusteeType}
}

func testACL(t *testing.T, testEntries ...testAccessEntry) *windows.ACL {
	t.Helper()
	entries := make([]windows.EXPLICIT_ACCESS, len(testEntries))
	var pinner runtime.Pinner
	defer pinner.Unpin()
	for i, entry := range testEntries {
		pinner.Pin(entry.sid)
		entries[i] = windows.EXPLICIT_ACCESS{
			AccessPermissions: entry.permissions,
			AccessMode:        windows.GRANT_ACCESS,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  entry.trusteeType,
				TrusteeValue: windows.TrusteeValueFromSID(entry.sid),
			},
		}
	}
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		t.Fatal(err)
	}
	return acl
}

func testSecurityDescriptor(t *testing.T, owner *windows.SID, dacl *windows.ACL, present bool) *windows.SECURITY_DESCRIPTOR {
	t.Helper()
	sd, err := windows.NewSecurityDescriptor()
	if err != nil {
		t.Fatal(err)
	}
	if err := sd.SetOwner(owner, false); err != nil {
		t.Fatal(err)
	}
	if err := sd.SetDACL(dacl, present, false); err != nil {
		t.Fatal(err)
	}
	relative, err := sd.ToSelfRelative()
	if err != nil {
		t.Fatal(err)
	}
	return relative
}

func testACE(t *testing.T, acl *windows.ACL) *windows.ACCESS_ALLOWED_ACE {
	t.Helper()
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(acl, 0, &ace); err != nil {
		t.Fatal(err)
	}
	return ace
}
