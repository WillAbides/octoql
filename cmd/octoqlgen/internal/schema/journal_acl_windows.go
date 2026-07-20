//go:build windows

package schema

import (
	"errors"
	"strings"

	"golang.org/x/sys/windows"
)

func verifyJournalACL(path string) error {
	currentUser, err := currentJournalUser()
	if err != nil {
		return err
	}
	securityDescriptor, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return err
	}
	dacl := daclString(securityDescriptor.String())
	expectedFileDACL := "D:P(A;;FA;;;" + currentUser.User.Sid.String() + ")"
	expectedDirectoryDACL := "D:P(A;OICI;FA;;;" + currentUser.User.Sid.String() + ")"
	if dacl != expectedFileDACL && dacl != expectedDirectoryDACL {
		return errors.New("journal DACL does not grant access exclusively to the current user")
	}
	return nil
}

func daclString(securityDescriptor string) string {
	index := strings.Index(securityDescriptor, "D:")
	if index == -1 {
		return ""
	}
	dacl := securityDescriptor[index:]
	saclIndex := strings.Index(dacl, "S:")
	if saclIndex == -1 {
		return dacl
	}
	return dacl[:saclIndex]
}
