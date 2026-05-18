// filex tiny utility — emit a JSON manifest of files in a directory.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

type entry struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

func main() {
	root := flag.String("root", ".", "directory to walk")
	flag.Parse()

	var list []entry
	err := filepath.WalkDir(*root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, _ := d.Info()
		list = append(list, entry{Path: p, Size: info.Size()})
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	json.NewEncoder(os.Stdout).Encode(list)
}
