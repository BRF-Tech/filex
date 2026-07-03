// Package webdav is a Storage Driver fronting a WebDAV server.
//
// Tested against Nextcloud, ownCloud, Apache mod_dav, nginx-dav, and
// SabreDAV. Authenticates via Basic auth — Bearer is V2.
package webdav

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/storage"
)

func init() {
	storage.Register("webdav", func() storage.Driver { return &Driver{} })
}

// Driver is the WebDAV storage driver.
type Driver struct {
	endpoint *url.URL
	user     string
	pass     string
	client   *http.Client
}

// Name implements storage.Driver.
func (d *Driver) Name() string { return "webdav" }

// Init configures the driver. Required: url, user, password.
func (d *Driver) Init(_ context.Context, cfg map[string]any) error {
	rawURL, _ := cfg["url"].(string)
	user, _ := cfg["user"].(string)
	pass, _ := cfg["password"].(string)
	if rawURL == "" || user == "" {
		return errors.New("webdav: url and user required")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("webdav: bad url: %w", err)
	}
	d.endpoint = u
	d.user = user
	d.pass = pass
	d.client = &http.Client{Timeout: 60 * time.Second}
	return nil
}

// Capabilities — WebDAV supports everything except Presign and Watch.
func (d *Driver) Capabilities() storage.Capabilities {
	return storage.Capabilities{
		Read:   true,
		Write:  true,
		Move:   true,
		Copy:   true,
		Delete: true,
		Mkdir:  true,
	}
}

func (d *Driver) urlFor(p string) string {
	clean := strings.TrimLeft(path.Clean("/"+p), "/")
	u := *d.endpoint
	u.Path = path.Join(u.Path, clean)
	return u.String()
}

func (d *Driver) do(req *http.Request) (*http.Response, error) {
	req.SetBasicAuth(d.user, d.pass)
	return d.client.Do(req)
}

// Standard PROPFIND XML body — Depth: 1.
const propfindBody = `<?xml version="1.0" encoding="utf-8" ?>
<D:propfind xmlns:D="DAV:">
  <D:prop>
    <D:displayname/>
    <D:getcontentlength/>
    <D:getlastmodified/>
    <D:getetag/>
    <D:getcontenttype/>
    <D:resourcetype/>
  </D:prop>
</D:propfind>`

// multistatusResponse models the WebDAV Multi-Status XML reply.
type multistatusResponse struct {
	XMLName   xml.Name `xml:"multistatus"`
	Responses []struct {
		Href     string `xml:"href"`
		Propstat struct {
			Prop struct {
				DisplayName  string `xml:"displayname"`
				ContentLen   string `xml:"getcontentlength"`
				LastModified string `xml:"getlastmodified"`
				ETag         string `xml:"getetag"`
				ContentType  string `xml:"getcontenttype"`
				ResourceType struct {
					Collection *struct{} `xml:"collection"`
				} `xml:"resourcetype"`
			} `xml:"prop"`
		} `xml:"propstat"`
	} `xml:"response"`
}

// List implements storage.Driver.
func (d *Driver) List(ctx context.Context, p string) ([]storage.Object, error) {
	req, _ := http.NewRequestWithContext(ctx, "PROPFIND", d.urlFor(p), strings.NewReader(propfindBody))
	req.Header.Set("Depth", "1")
	req.Header.Set("Content-Type", `application/xml; charset="utf-8"`)
	resp, err := d.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, storage.ErrNotFound
	}
	if resp.StatusCode != http.StatusMultiStatus {
		return nil, fmt.Errorf("webdav: list http %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var ms multistatusResponse
	if err := xml.Unmarshal(body, &ms); err != nil {
		return nil, fmt.Errorf("webdav: parse: %w", err)
	}
	out := make([]storage.Object, 0, len(ms.Responses))
	parentClean := strings.TrimLeft(path.Clean("/"+p), "/")
	for _, r := range ms.Responses {
		href, _ := url.QueryUnescape(r.Href)
		hrefPath := strings.Trim(strings.TrimPrefix(href, d.endpoint.Path), "/")
		if hrefPath == parentClean {
			continue
		}
		name := path.Base(hrefPath)
		if name == "" {
			continue
		}
		obj := storage.Object{
			Path: path.Join(p, name),
			Name: name,
			Etag: r.Propstat.Prop.ETag,
			Mime: r.Propstat.Prop.ContentType,
		}
		if r.Propstat.Prop.ResourceType.Collection != nil {
			obj.Kind = storage.KindDirectory
		} else {
			obj.Kind = storage.KindFile
			if n, err := strconv.ParseInt(r.Propstat.Prop.ContentLen, 10, 64); err == nil {
				obj.Size = n
			}
			if t, err := time.Parse(time.RFC1123, r.Propstat.Prop.LastModified); err == nil {
				obj.Mtime = t
			}
		}
		out = append(out, obj)
	}
	return out, nil
}

// Stat implements storage.Driver — emulated via PROPFIND Depth:0.
func (d *Driver) Stat(ctx context.Context, p string) (storage.Object, error) {
	req, _ := http.NewRequestWithContext(ctx, "PROPFIND", d.urlFor(p), strings.NewReader(propfindBody))
	req.Header.Set("Depth", "0")
	req.Header.Set("Content-Type", `application/xml; charset="utf-8"`)
	resp, err := d.do(req)
	if err != nil {
		return storage.Object{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return storage.Object{}, storage.ErrNotFound
	}
	body, _ := io.ReadAll(resp.Body)
	var ms multistatusResponse
	if err := xml.Unmarshal(body, &ms); err != nil {
		return storage.Object{}, err
	}
	if len(ms.Responses) == 0 {
		return storage.Object{}, storage.ErrNotFound
	}
	r := ms.Responses[0]
	obj := storage.Object{
		Path: p,
		Name: path.Base(p),
		Etag: r.Propstat.Prop.ETag,
		Mime: r.Propstat.Prop.ContentType,
	}
	if r.Propstat.Prop.ResourceType.Collection != nil {
		obj.Kind = storage.KindDirectory
	} else {
		obj.Kind = storage.KindFile
		if n, err := strconv.ParseInt(r.Propstat.Prop.ContentLen, 10, 64); err == nil {
			obj.Size = n
		}
		if t, err := time.Parse(time.RFC1123, r.Propstat.Prop.LastModified); err == nil {
			obj.Mtime = t
		}
	}
	return obj, nil
}

// Read implements storage.Driver.
func (d *Driver) Read(ctx context.Context, p string) (io.ReadCloser, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", d.urlFor(p), nil)
	resp, err := d.do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, storage.ErrNotFound
	}
	if resp.StatusCode/100 != 2 {
		resp.Body.Close()
		return nil, fmt.Errorf("webdav: read http %d", resp.StatusCode)
	}
	return resp.Body, nil
}

// Write implements storage.Writer.
func (d *Driver) Write(ctx context.Context, p string, r io.Reader, _ int64) error {
	req, _ := http.NewRequestWithContext(ctx, "PUT", d.urlFor(p), r)
	resp, err := d.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("webdav: write http %d", resp.StatusCode)
	}
	return nil
}

// Move implements storage.Mover.
func (d *Driver) Move(ctx context.Context, src, dst string) error {
	req, _ := http.NewRequestWithContext(ctx, "MOVE", d.urlFor(src), nil)
	req.Header.Set("Destination", d.urlFor(dst))
	req.Header.Set("Overwrite", "T")
	resp, err := d.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("webdav: move http %d", resp.StatusCode)
	}
	return nil
}

// Copy implements storage.Copier.
func (d *Driver) Copy(ctx context.Context, src, dst string) error {
	req, _ := http.NewRequestWithContext(ctx, "COPY", d.urlFor(src), nil)
	req.Header.Set("Destination", d.urlFor(dst))
	req.Header.Set("Overwrite", "T")
	resp, err := d.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("webdav: copy http %d", resp.StatusCode)
	}
	return nil
}

// Delete implements storage.Deleter.
func (d *Driver) Delete(ctx context.Context, p string) error {
	req, _ := http.NewRequestWithContext(ctx, "DELETE", d.urlFor(p), nil)
	resp, err := d.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("webdav: delete http %d", resp.StatusCode)
	}
	return nil
}

// Mkdir implements storage.Mkdirer (MKCOL).
func (d *Driver) Mkdir(ctx context.Context, p string) error {
	req, _ := http.NewRequestWithContext(ctx, "MKCOL", d.urlFor(p), bytes.NewReader(nil))
	resp, err := d.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusMethodNotAllowed {
		// already exists
		return nil
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("webdav: mkdir http %d", resp.StatusCode)
	}
	return nil
}
