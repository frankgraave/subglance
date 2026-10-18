//go:build !unix

package main

// requireDataAccess checks nothing outside Unix. On Windows a file's Go mode
// only says whether it is read-only, and access is decided by the folder's
// permission list, which the error from the first open already names.
func requireDataAccess(string, []dataFile) error { return nil }
