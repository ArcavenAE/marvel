package company

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// TreeHash is the tree hash v1 of the version directory dir
// (docs/design/companies.md, "The tree hash (v1)"). Every file, directory and
// symbolic link under dir is an entry and nothing is followed. A record is
// <type> NUL <path> NUL <value> NUL, with type f, d or l, the path relative to
// dir in the raw bytes on disk, and the value the hex sha256 of a file's bytes,
// empty for a directory, and a link's target as readlink returns it. Records
// are sorted by path bytes and hashed together. Mode bits, owners and times are
// not part of it. Anything under dir that is not a file, directory or link is
// an error, since no record describes it.
func TreeHash(dir string) (string, error) {
	type record struct{ path, text string }
	var recs []record
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == dir {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		switch typ := d.Type(); {
		case typ&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			recs = append(recs, record{rel, "l\x00" + rel + "\x00" + target + "\x00"})
		case typ.IsDir():
			recs = append(recs, record{rel, "d\x00" + rel + "\x00\x00"})
		case typ.IsRegular():
			sum, err := fileSum(p)
			if err != nil {
				return err
			}
			recs = append(recs, record{rel, "f\x00" + rel + "\x00" + sum + "\x00"})
		default:
			return fmt.Errorf("tree hash: %s is not a file, a directory or a link", quoteField(rel, false))
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("tree hash: %w", err)
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].path < recs[j].path })
	h := sha256.New()
	for _, r := range recs {
		_, _ = io.WriteString(h, r.text)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func fileSum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
