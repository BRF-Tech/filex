// filex tiny utility — emit a JSON manifest of files in a directory.
//
// Walks the given root, collects (path, size, mode, mtime) for every
// file, and writes a JSON array to stdout. Used by the demo notebooks
// to populate an in-memory dataframe.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type entry struct {
	Path  string    `json:"path"`
	Size  int64     `json:"size"`
	Mode  string    `json:"mode"`
	Mtime time.Time `json:"mtime"`
}

func main() {
	root := flag.String("root", ".", "directory to walk")
	prefix := flag.String("prefix", "", "strip this prefix from every emitted path")
	flag.Parse()

	var list []entry
	err := filepath.WalkDir(*root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, _ := d.Info()
		name := p
		if *prefix != "" {
			if rel, rerr := filepath.Rel(*prefix, p); rerr == nil {
				name = rel
			}
		}
		list = append(list, entry{Path: name, Size: info.Size(), Mode: info.Mode().String(), Mtime: info.ModTime()})
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Size > list[j].Size })
	json.NewEncoder(os.Stdout).Encode(list)
}
