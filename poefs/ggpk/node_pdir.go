package ggpk

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/aviddiviner/go-murmur"
)

type pdirNode struct {
	src       *ggpkFS
	name      string
	signature []byte
	children  []pdirChild
}

type pdirChild struct {
	offset int64
	hash   uint32
}

func (n *pdirNode) Name() string {
	return n.name
}

func (g *ggpkFS) newPdirNode(data []byte, offset int64, length uint32) (*pdirNode, error) {
	if len(data) < 40 {
		return nil, errNodeTooShort
	}

	nameLength := int(binary.LittleEndian.Uint32(data[0:]))
	childCount := int(binary.LittleEndian.Uint32(data[4:]))
	signature := data[8:40]

	if int(length) != 40+g.sizeofChars(nameLength)+12*childCount {
		return nil, errNodeTooShort
	}

	if len(data) < int(length) {
		data = make([]byte, length)
		_, err := g.file.ReadAt(data, offset)
		if err != nil {
			return nil, fmt.Errorf("failed to read PDIR contents: %w", err)
		}
	}

	br := bytes.NewReader(data[40:])
	name, err := g.readStringFrom(br)
	if err != nil {
		return nil, fmt.Errorf("failed to read PDIR name: %w", err)
	}

	n := &pdirNode{
		src:       g,
		name:      name,
		signature: signature,
		children:  make([]pdirChild, childCount),
	}

	for i := range n.children {
		tmp := struct {
			Hash   uint32
			Offset int64
		}{}
		err := binary.Read(br, binary.LittleEndian, &tmp)
		if err != nil {
			return nil, fmt.Errorf("failed to read PDIR child %d: %w", i, err)
		}
		n.children[i] = pdirChild{
			offset: tmp.Offset,
			hash:   tmp.Hash,
		}
	}

	return n, nil
}

func (n *pdirNode) Children() ([]anyNode, error) {
	children := make([]anyNode, len(n.children))
	for i := range children {
		c, err := n.src.getNodeAt(n.children[i].offset)
		if err != nil {
			return nil, err
		}
		children[i] = c
	}
	return children, nil
}

func (n *pdirNode) ChildNamed(name string) (anyNode, error) {
	codepoints := utf16.Encode([]rune(strings.ToLower(name)))
	var cp []byte
	buf := new(bytes.Buffer)
	for _, c := range codepoints {
		err := binary.Write(buf, binary.LittleEndian, c)
		if err != nil {
			return nil, err
		}
	}
	cp = buf.Bytes()
	h := murmur.MurmurHash2(cp, 0x0)
	for i := range n.children {
		if n.children[i].hash == h {
			cn, err := n.src.getNodeAt(n.children[i].offset)
			if err != nil {
				return nil, err
			}
			if cn.Name() == name {
				return cn, nil
			}
		}
	}
	return nil, fs.ErrNotExist
}

func (n *pdirNode) Reader() (fs.File, error) {
	return &fsPdirNode{n, 0}, nil
}

// fsPdirNode

type fsPdirNode struct {
	*pdirNode
	offset int
}

func (fpn *fsPdirNode) Read([]byte) (int, error) {
	return 0, errNotFile
}

func (fpn *fsPdirNode) Close() error {
	return nil
}

func (fpn *fsPdirNode) Stat() (fs.FileInfo, error) {
	return &fsPdirNodeStat{fpn}, nil
}

func (fpn *fsPdirNode) ReadDir(n int) ([]fs.DirEntry, error) {
	children, err := fpn.Children()
	if err != nil {
		return nil, err
	}

	dirents := make([]fs.DirEntry, len(children))
	for i, c := range children {
		dirents[i] = &fsDirEnt{c}
	}

	if n <= 0 {
		fpn.offset = 0
		return dirents, nil
	}

	dirents = dirents[fpn.offset:]
	if len(dirents) > n {
		fpn.offset += n
		return dirents[:n], nil
	} else {
		fpn.offset += len(dirents)
		return dirents, io.EOF
	}
}

// fsDirEnt

type fsDirEnt struct {
	n anyNode
}

func (fde *fsDirEnt) Name() string {
	return fde.n.Name()
}

func (fde *fsDirEnt) IsDir() bool {
	_, ok := fde.n.(*pdirNode)
	return ok
}

func (fde *fsDirEnt) Type() fs.FileMode {
	if fde.IsDir() {
		return 0o444 | fs.ModeDir
	} else {
		return 0o444
	}
}

func (fde *fsDirEnt) Info() (fs.FileInfo, error) {
	switch n := fde.n.(type) {
	case *fileNode:
		r, err := n.Reader()
		if err != nil {
			return nil, err
		}
		return r.Stat()
	case *pdirNode:
		return (&fsPdirNode{n, 0}).Stat()
	default:
		panic("dirent is neither file nor pdir")
	}
}

// fsPdirNodeStat

type fsPdirNodeStat struct {
	*fsPdirNode
}

func (fps *fsPdirNodeStat) Name() string {
	return fps.name
}

func (fps *fsPdirNodeStat) Size() int64 {
	return 0
}

func (fps *fsPdirNodeStat) Mode() fs.FileMode {
	return 0o444 | fs.ModeDir
}

func (ffi *fsPdirNodeStat) ModTime() time.Time {
	return time.Unix(0, 0)
}

func (ffi *fsPdirNodeStat) IsDir() bool {
	return true
}

func (ffi *fsPdirNodeStat) Sys() any {
	return nil
}

func (ffi *fsPdirNodeStat) Provenance() string {
	return "GGPK"
}

func (ffi *fsPdirNodeStat) Signature() []byte {
	return ffi.signature
}
