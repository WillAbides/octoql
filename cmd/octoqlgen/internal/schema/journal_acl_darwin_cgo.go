//go:build darwin && cgo

package schema

/*
#include <errno.h>
#include <stdlib.h>
#include <sys/acl.h>

int octoql_acl_has_allow_entry_and_free(char *path) {
	acl_t acl = acl_get_file(path, ACL_TYPE_EXTENDED);
	free(path);
	if (acl == NULL) {
		return -errno;
	}

	acl_entry_t entry;
	int entry_id = ACL_FIRST_ENTRY;
	int result;
	while ((result = acl_get_entry(acl, entry_id, &entry)) == 0) {
		acl_tag_t tag;
		if (acl_get_tag_type(entry, &tag) != 0) {
			int err = errno;
			acl_free(acl);
			return -err;
		}
		if (tag == ACL_EXTENDED_ALLOW) {
			acl_free(acl);
			return 1;
		}
		entry_id = ACL_NEXT_ENTRY;
	}
	if (result == -1 && errno != EINVAL) {
		int err = errno;
		acl_free(acl);
		return -err;
	}

	acl_free(acl);
	return 0;
}
*/
import "C"

import (
	"errors"
	"syscall"
)

func verifyJournalACL(path string) error {
	result := C.octoql_acl_has_allow_entry_and_free(C.CString(path))
	if result == 1 {
		return errors.New("non-POSIX ACL is not allowed")
	}
	if result == 0 {
		return nil
	}
	err := syscall.Errno(-result)
	if errors.Is(err, syscall.ENOENT) {
		return nil
	}
	return err
}
