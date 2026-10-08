package e2edecrypt

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/brf-tech/filex/backend/internal/e2e"
)

// The tree of a vault: what its index holds, in memory. Pure bookkeeping, no
// cryptography - the writer (vault_write.go), the reader (vault_read.go) and
// `filex vault mount`'s own view of the tree all use it.
//
// Paths are relative to the vault root, segments separated by "/": "" is the
// root, "Belgeler/Arşiv" a folder in a folder. A leading "/" is accepted.

var (
	// ErrVaultNotFound: no entry at that path.
	ErrVaultNotFound = errors.New("no such file or folder in the vault")
	// ErrVaultExists: the name is taken in that folder.
	ErrVaultExists = errors.New("a file or folder with that name is already there")
	// ErrVaultNotDir: a path segment that should be a folder is a file.
	ErrVaultNotDir = errors.New("not a folder")
	// ErrVaultIsDir: a file operation on a folder.
	ErrVaultIsDir = errors.New("is a folder")
	// ErrVaultBadName: a name the format does not allow.
	ErrVaultBadName = errors.New("not a name a vault can hold")
	// ErrVaultFull: the change would pass the writer's entry limit.
	ErrVaultFull = fmt.Errorf("this vault already holds the most files and folders a vault may hold (%d)", e2e.VaultEntriesWriteMax)
	// ErrVaultTooDeep: the change would nest folders deeper than the format allows.
	ErrVaultTooDeep = fmt.Errorf("folders in a vault nest at most %d deep", e2e.VaultMaxDepth)
	// ErrVaultLoop: a folder moved into itself.
	ErrVaultLoop = errors.New("a folder cannot be moved into itself")
)

// VaultExtent is a run of a file's encrypted body inside one pack.
type VaultExtent struct {
	Pack   [16]byte
	Offset int64
	Length int64
}

// VaultContent is where one version of a file is stored. A VaultContent is
// never changed once it is in a tree: an operation that moves bytes builds a
// new one, so trees can share them.
type VaultContent struct {
	ID        [16]byte
	ChunkLog2 int
	Extents   []VaultExtent
}

// BodyLen is the length of the encrypted body: the extents joined.
func (c *VaultContent) BodyLen() int64 {
	var n int64
	for _, x := range c.Extents {
		n += x.Length
	}
	return n
}

// VaultNode is one entry of a vault tree.
type VaultNode struct {
	Name  string
	Dir   bool
	MTime int64 // milliseconds since 1970
	Size  int64 // a file's plaintext bytes
	// Content is where a file's bytes are; nil for a folder, a 0-byte file,
	// or a file whose bytes are not stored yet (Local).
	Content *VaultContent
	// Local is the caller's own (`filex vault mount` keeps a pending file's
	// spool here). It is never encoded.
	Local any

	ext      []byte
	parent   *VaultNode
	children map[string]*VaultNode
}

// Parent is the folder the entry is in; nil for the root.
func (n *VaultNode) Parent() *VaultNode { return n.parent }

// Child is the entry called name directly inside n, or nil.
func (n *VaultNode) Child(name string) *VaultNode {
	if n == nil || n.children == nil {
		return nil
	}
	return n.children[name]
}

// Children are the entries directly inside n, in canonical order (by the
// bytes of their names).
func (n *VaultNode) Children() []*VaultNode {
	out := make([]*VaultNode, 0, len(n.children))
	for _, c := range n.children {
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b *VaultNode) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// Path is the entry's path from the vault root ("" for the root).
func (n *VaultNode) Path() string {
	if n == nil || n.parent == nil {
		return ""
	}
	var parts []string
	for p := n; p.parent != nil; p = p.parent {
		parts = append(parts, p.Name)
	}
	slices.Reverse(parts)
	return strings.Join(parts, "/")
}

// HasExt reports whether the entry carries extension bytes a newer filex
// wrote.
func (n *VaultNode) HasExt() bool { return len(n.ext) > 0 }

// attached reports whether n is still in a tree (its chain of parents ends
// at a root).
func (n *VaultNode) attached(root *VaultNode) bool {
	p := n
	for p.parent != nil {
		if p.parent.children[p.Name] != p {
			return false
		}
		p = p.parent
	}
	return p == root
}

func (n *VaultNode) depth() int {
	d := 0
	for p := n; p.parent != nil; p = p.parent {
		d++
	}
	return d
}

// height is how many levels n and what is under it take (a file: 1).
func (n *VaultNode) height() int {
	h := 0
	for _, c := range n.children {
		h = max(h, c.height())
	}
	return h + 1
}

func (n *VaultNode) subtreeLen() int {
	c := 1
	for _, k := range n.children {
		c += k.subtreeLen()
	}
	return c
}

// VaultTree is a vault's tree of files and folders.
type VaultTree struct {
	root  *VaultNode
	count int
}

// NewVaultTree is an empty tree.
func NewVaultTree() *VaultTree { return &VaultTree{root: &VaultNode{Dir: true}} }

// Root is the vault folder itself.
func (t *VaultTree) Root() *VaultNode { return t.root }

// Len is the number of entries (files and folders), the root not counted.
func (t *VaultTree) Len() int { return t.count }

// Clone copies the tree. Contents are shared (they never change in place);
// Local is copied as it is.
func (t *VaultTree) Clone() *VaultTree {
	return &VaultTree{root: cloneVaultNode(t.root, nil), count: t.count}
}

func cloneVaultNode(n, parent *VaultNode) *VaultNode {
	c := *n
	c.parent = parent
	c.children = nil
	if n.ext != nil {
		c.ext = slices.Clone(n.ext)
	}
	if len(n.children) > 0 {
		c.children = make(map[string]*VaultNode, len(n.children))
		for name, k := range n.children {
			c.children[name] = cloneVaultNode(k, &c)
		}
	}
	return &c
}

// Walk visits every entry in canonical order - pre-order, siblings by the
// bytes of their names - with its depth (the root's children are depth 1).
func (t *VaultTree) Walk(fn func(n *VaultNode, depth int) error) error {
	var walk func(n *VaultNode, depth int) error
	walk = func(n *VaultNode, depth int) error {
		for _, c := range n.Children() {
			if err := fn(c, depth); err != nil {
				return err
			}
			if c.Dir {
				if err := walk(c, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(t.root, 1)
}

// vaultSegments splits a path. Empty segments ("a//b") are refused.
func vaultSegments(p string) ([]string, error) {
	p = strings.TrimPrefix(p, "/")
	p = strings.TrimSuffix(p, "/")
	if p == "" {
		return nil, nil
	}
	segs := strings.Split(p, "/")
	for _, s := range segs {
		if s == "" {
			return nil, fmt.Errorf("%q: %w", p, ErrVaultBadName)
		}
	}
	return segs, nil
}

// vaultNameProblem is the format's rule for a name (docs/E2E-VAULT-FORMAT.md
// → "Index body", `name`): UTF-8, 1 to 255 bytes, not "." or "..", no "/",
// "\", control byte or DEL. "" when the name is fine.
func vaultNameProblem(name string) string {
	if !utf8.ValidString(name) {
		return "not UTF-8"
	}
	return NameProblem(name)
}

// childOf finds a child by its stored name, or by its NFC form when a name
// arrives in another normalisation (macOS sends NFD).
func childOf(dir *VaultNode, name string) *VaultNode {
	if c := dir.Child(name); c != nil {
		return c
	}
	if nfc := norm.NFC.String(name); nfc != name {
		return dir.Child(nfc)
	}
	return nil
}

// Lookup finds the entry at p; nil when there is none. "" is the root.
func (t *VaultTree) Lookup(p string) *VaultNode {
	segs, err := vaultSegments(p)
	if err != nil {
		return nil
	}
	n := t.root
	for _, s := range segs {
		if !n.Dir {
			return nil
		}
		if n = childOf(n, s); n == nil {
			return nil
		}
	}
	return n
}

// place resolves where a new entry at p goes: its folder, and its name in
// NFC (writers store names normalised). The folder must exist.
func (t *VaultTree) place(p string) (*VaultNode, string, error) {
	segs, err := vaultSegments(p)
	if err != nil {
		return nil, "", err
	}
	if len(segs) == 0 {
		return nil, "", fmt.Errorf("the vault root: %w", ErrVaultBadName)
	}
	dir := t.root
	for _, s := range segs[:len(segs)-1] {
		c := childOf(dir, s)
		if c == nil {
			return nil, "", fmt.Errorf("%s: %w", p, ErrVaultNotFound)
		}
		if !c.Dir {
			return nil, "", fmt.Errorf("%s: %w", c.Path(), ErrVaultNotDir)
		}
		dir = c
	}
	name := norm.NFC.String(segs[len(segs)-1])
	if prob := vaultNameProblem(name); prob != "" {
		return nil, "", fmt.Errorf("%q (%s): %w", name, prob, ErrVaultBadName)
	}
	return dir, name, nil
}

func (t *VaultTree) attach(dir *VaultNode, n *VaultNode) {
	if dir.children == nil {
		dir.children = map[string]*VaultNode{}
	}
	n.parent = dir
	dir.children[n.Name] = n
}

// CheckMkdir reports whether Mkdir(p) would succeed, without changing anything.
func (t *VaultTree) CheckMkdir(p string) error {
	dir, name, err := t.place(p)
	if err != nil {
		return err
	}
	if childOf(dir, name) != nil {
		return fmt.Errorf("%s: %w", p, ErrVaultExists)
	}
	if dir.depth()+1 > e2e.VaultMaxDepth {
		return ErrVaultTooDeep
	}
	return nil
}

// Mkdir makes a new folder.
func (t *VaultTree) Mkdir(p string, mtime int64) (*VaultNode, error) {
	if err := t.CheckMkdir(p); err != nil {
		return nil, err
	}
	dir, name, _ := t.place(p)
	n := &VaultNode{Name: name, Dir: true, MTime: max(mtime, 0)}
	t.attach(dir, n)
	t.count++
	return n, nil
}

// CheckPutFile reports whether PutFile(p) would succeed, and whether it
// would add an entry (false: it replaces a file).
func (t *VaultTree) CheckPutFile(p string) (adds bool, err error) {
	dir, name, err := t.place(p)
	if err != nil {
		return false, err
	}
	if old := childOf(dir, name); old != nil {
		if old.Dir {
			return false, fmt.Errorf("%s: %w", p, ErrVaultIsDir)
		}
		return false, nil
	}
	if dir.depth()+1 > e2e.VaultMaxDepth {
		return false, ErrVaultTooDeep
	}
	return true, nil
}

// PutFile makes a new file, or gives the file at p new contents: nothing of
// the old version is kept but its place in the tree. Local is cleared.
func (t *VaultTree) PutFile(p string, mtime, size int64, c *VaultContent) (*VaultNode, error) {
	if _, err := t.CheckPutFile(p); err != nil {
		return nil, err
	}
	// size > 0 without content is a file whose bytes are not stored yet (the
	// mount's view); content without bytes is never right.
	if size < 0 || (size == 0 && c != nil) {
		return nil, errors.New("e2edecrypt: a file's size and its content disagree")
	}
	dir, name, _ := t.place(p)
	if old := childOf(dir, name); old != nil {
		old.MTime, old.Size, old.Content, old.Local, old.ext = max(mtime, 0), size, c, nil, nil
		return old, nil
	}
	n := &VaultNode{Name: name, MTime: max(mtime, 0), Size: size, Content: c}
	t.attach(dir, n)
	t.count++
	return n, nil
}

// Remove takes the entry at p out of the tree, a folder with everything in it.
func (t *VaultTree) Remove(p string) (*VaultNode, error) {
	n := t.Lookup(p)
	if n == nil {
		return nil, fmt.Errorf("%s: %w", p, ErrVaultNotFound)
	}
	if n == t.root {
		return nil, fmt.Errorf("the vault root: %w", ErrVaultBadName)
	}
	delete(n.parent.children, n.Name)
	t.count -= n.subtreeLen()
	return n, nil
}

// CheckMove reports whether Move(from, to) would succeed.
func (t *VaultTree) CheckMove(from, to string) error {
	n := t.Lookup(from)
	if n == nil {
		return fmt.Errorf("%s: %w", from, ErrVaultNotFound)
	}
	if n == t.root {
		return fmt.Errorf("the vault root: %w", ErrVaultBadName)
	}
	dir, name, err := t.place(to)
	if err != nil {
		return err
	}
	for p := dir; p != nil; p = p.parent {
		if p == n {
			return ErrVaultLoop
		}
	}
	if c := childOf(dir, name); c != nil && c != n {
		return fmt.Errorf("%s: %w", to, ErrVaultExists)
	}
	if dir.depth()+n.height() > e2e.VaultMaxDepth {
		return ErrVaultTooDeep
	}
	return nil
}

// Move gives the entry at from a new folder or name; its mtime, its contents
// and (a folder) everything in it are kept.
func (t *VaultTree) Move(from, to string) (*VaultNode, error) {
	if err := t.CheckMove(from, to); err != nil {
		return nil, err
	}
	n := t.Lookup(from)
	dir, name, _ := t.place(to)
	delete(n.parent.children, n.Name)
	n.Name = name
	t.attach(dir, n)
	return n, nil
}

// packTable is every pack an extent of the tree uses, each once, in ascending
// byte order.
func (t *VaultTree) packTable() [][16]byte {
	seen := map[[16]byte]bool{}
	var out [][16]byte
	_ = t.Walk(func(n *VaultNode, _ int) error {
		if n.Content == nil {
			return nil
		}
		for _, x := range n.Content.Extents {
			if !seen[x.Pack] {
				seen[x.Pack] = true
				out = append(out, x.Pack)
			}
		}
		return nil
	})
	slices.SortFunc(out, compareVaultIDs)
	return out
}

func compareVaultIDs(a, b [16]byte) int { return strings.Compare(string(a[:]), string(b[:])) }
