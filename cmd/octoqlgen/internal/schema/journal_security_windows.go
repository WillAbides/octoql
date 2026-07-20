//go:build windows

package schema

import (
	"fmt"

	"golang.org/x/sys/windows"
)

func secureJournalDirectory(path string) error {
	return secureJournalPath(path, "D:P(A;OICI;FA;;;")
}

func secureJournalFile(path string) error {
	return secureJournalPath(path, "D:P(A;;FA;;;")
}

func secureJournalPath(path, descriptorPrefix string) error {
	currentUser, err := currentJournalUser()
	if err != nil {
		return err
	}
	securityDescriptor, err := windows.SecurityDescriptorFromString(
		descriptorPrefix + currentUser.User.Sid.String() + ")",
	)
	if err != nil {
		return err
	}
	dacl, _, err := securityDescriptor.DACL()
	if err != nil {
		return err
	}
	err = windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	)
	if err != nil {
		return fmt.Errorf("setting private journal ACL: %w", err)
	}
	return nil
}
