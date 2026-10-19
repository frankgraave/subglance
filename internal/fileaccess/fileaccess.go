// Package fileaccess words the refusal of a file SubGlance was told to read
// and may not, so that the message names the owner, the user SubGlance runs
// as and the command that fixes it, instead of a bare "permission denied".
//
// The case it exists for is a secret mounted into the container. The image
// runs as an unprivileged user, a bind-mounted file keeps its owner on the
// host (usually root), and the documentation itself asks for that file to be
// readable by its owner only. Before this package the first start after
// following those instructions ended in "permission denied" with nothing to
// say whose permission, or what to change.
package fileaccess
