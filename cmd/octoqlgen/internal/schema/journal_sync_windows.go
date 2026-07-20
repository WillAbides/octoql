//go:build windows

package schema

func syncDirectory(string) error {
	// Windows directory handles cannot be flushed. renameFileAtomically uses
	// MOVEFILE_WRITE_THROUGH so journal publication remains ordered.
	return nil
}
