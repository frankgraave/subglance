//go:build !unix

package fileaccess

// RequireReadable checks nothing outside Unix. On Windows a file's Go mode
// only says whether it is read-only, and access is decided by the folder's
// permission list, which the error from the read that follows already names.
func RequireReadable(string, string) error { return nil }
