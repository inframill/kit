// Package filetree defines internal immutable file-tree records.
package filetree

// File represents one relative tree entry; directories contain no bytes.
type File struct {
	Content    []byte
	Directory  bool
	Executable bool
}
