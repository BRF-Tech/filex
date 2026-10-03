package assoc

import (
	"sort"
	"strings"
)

// knownKinds is the media type of each extension the screens name, and the
// table an app's media types are turned into kinds with (`image/*` lists
// every image kind here). A fixed table, not the operating system's
// (Go's mime package reads /etc/mime.types on Linux): the list of kinds an
// administrator sees must not depend on which server answered.
var knownKinds = map[string]string{
	// images
	"jpg": "image/jpeg", "jpeg": "image/jpeg", "png": "image/png", "gif": "image/gif",
	"webp": "image/webp", "bmp": "image/bmp", "tif": "image/tiff", "tiff": "image/tiff",
	"svg": "image/svg+xml", "heic": "image/heic", "heif": "image/heif", "avif": "image/avif",
	"ico": "image/x-icon", "psd": "image/vnd.adobe.photoshop", "jxl": "image/jxl",
	"dng": "image/x-adobe-dng", "cr2": "image/x-canon-cr2", "nef": "image/x-nikon-nef", "arw": "image/x-sony-arw",
	// video
	"mp4": "video/mp4", "m4v": "video/mp4", "webm": "video/webm", "mov": "video/quicktime",
	"mkv": "video/x-matroska", "avi": "video/x-msvideo", "ogv": "video/ogg", "wmv": "video/x-ms-wmv",
	// audio
	"mp3": "audio/mpeg", "wav": "audio/wav", "ogg": "audio/ogg", "flac": "audio/flac",
	"m4a": "audio/mp4", "aac": "audio/aac", "opus": "audio/opus", "wma": "audio/x-ms-wma",
	// documents
	"pdf": "application/pdf", "doc": "application/msword",
	"docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	"xls":  "application/vnd.ms-excel",
	"xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	"ppt":  "application/vnd.ms-powerpoint",
	"pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	"odt":  "application/vnd.oasis.opendocument.text",
	"ods":  "application/vnd.oasis.opendocument.spreadsheet",
	"odp":  "application/vnd.oasis.opendocument.presentation",
	"rtf":  "application/rtf", "epub": "application/epub+zip",
	// text and code
	"txt": "text/plain", "md": "text/markdown", "csv": "text/csv", "tsv": "text/tab-separated-values",
	"html": "text/html", "htm": "text/html", "css": "text/css", "js": "text/javascript",
	"json": "application/json", "xml": "application/xml", "yaml": "application/yaml", "yml": "application/yaml",
	"log": "text/plain", "ini": "text/plain", "toml": "application/toml",
	// archives and packages
	"zip": "application/zip", "tar": "application/x-tar", "gz": "application/gzip", "tgz": "application/gzip",
	"bz2": "application/x-bzip2", "xz": "application/x-xz", "7z": "application/x-7z-compressed",
	"rar": "application/vnd.rar", "zst": "application/zstd",
	"jar": "application/java-archive", "apk": "application/vnd.android.package-archive",
	"whl": "application/zip", "vsix": "application/zip", "nupkg": "application/zip", "xpi": "application/x-xpinstall",
	"deb": "application/vnd.debian.binary-package", "rpm": "application/x-rpm", "iso": "application/x-iso9660-image",
	// design and diagrams
	"drawio": "application/vnd.jgraph.mxfile", "dio": "application/vnd.jgraph.mxfile",
	"excalidraw": "application/vnd.excalidraw+json", "fig": "application/x-figma",
	"stl": "model/stl", "obj": "model/obj", "glb": "model/gltf-binary", "gltf": "model/gltf+json",
	// fonts
	"ttf": "font/ttf", "otf": "font/otf", "woff": "font/woff", "woff2": "font/woff2",
}

// MimeOf is the media type the screens give a kind ("" when unknown).
func MimeOf(ext string) string { return knownKinds[strings.ToLower(ext)] }

// ExtsOfMime is every known kind of a media type (exact) or a family
// (`image/*`), sorted. `*/*` names none: an app may not claim every kind.
func ExtsOfMime(m string) []string {
	m = normMime(m)
	if m == "" || m == "*/*" {
		return nil
	}
	family := strings.HasSuffix(m, "/*")
	prefix := strings.TrimSuffix(m, "*")
	var out []string
	for ext, t := range knownKinds {
		if t == m || (family && strings.HasPrefix(t, prefix)) {
			out = append(out, ext)
		}
	}
	sort.Strings(out)
	return out
}
