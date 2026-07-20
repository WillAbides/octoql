//go:build windows

package schema

import (
	"fmt"
	"io/fs"

	"golang.org/x/sys/windows"
)

func verifyJournalOwner(path string, _ fs.FileInfo) error {
	currentUser, err := currentJournalUser()
	if err != nil {
		return err
	}
	securityDescriptor, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION,
	)
	if err != nil {
		return err
	}
	owner, _, err := securityDescriptor.Owner()
	if err != nil {
		return err
	}
	if !owner.Equals(currentUser.User.Sid) {
		return fmt.Errorf("journal is not owned by the current user")
	}
	return nil
}

func verifyJournalParentOwner(path string, info fs.FileInfo) error {
	return verifyJournalOwner(path, info)
}

func currentJournalUser() (*windows.Tokenuser, error) {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return nil, err
	}
	defer token.Close()
	return token.GetTokenUser()
}
