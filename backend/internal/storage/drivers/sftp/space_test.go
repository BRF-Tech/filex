package sftp

import (
	"context"
	"strings"
	"testing"

	"github.com/pkg/sftp"

	"github.com/brf-tech/filex/backend/internal/storage"
)

var _ storage.SpaceReporter = (*Driver)(nil)

// roomCmder is the fake server's command handler with statvfs@openssh.com on
// top: what OpenSSH answers for a real disk.
type roomCmder struct {
	sftp.FileCmder
	vfs sftp.StatVFS
}

func (r roomCmder) StatVFS(*sftp.Request) (*sftp.StatVFS, error) {
	v := r.vfs
	return &v, nil
}

func TestSpace_AsksTheServersDisk(t *testing.T) {
	s := newFakeSFTP(t)
	s.handlers.FileCmd = roomCmder{FileCmder: s.handlers.FileCmd, vfs: sftp.StatVFS{
		Bsize: 4096, Frsize: 4096, Blocks: 1000, Bfree: 400, Bavail: 350, Fsid: 7, Namemax: 255,
	}}
	d := sftpDriver(t, s.addr(), nil)

	sp, err := d.Space(context.Background(), "")
	if err != nil {
		t.Fatalf("Space: %v", err)
	}
	if sp.Total != 1000*4096 || sp.Used != 600*4096 || sp.Free != 350*4096 {
		t.Errorf("got total=%d used=%d free=%d, want %d %d %d", sp.Total, sp.Used, sp.Free, 1000*4096, 600*4096, 350*4096)
	}
	if !strings.HasSuffix(sp.Volume, "#7") {
		t.Errorf("volume %q does not carry the server's fsid", sp.Volume)
	}
}

// plainCmder is a command handler with nothing but FileCmder's own method: a
// server that does not speak statvfs@openssh.com. (pkg/sftp's in-memory
// handler does speak it, answering with the test machine's own disk.)
type plainCmder struct{ sftp.FileCmder }

// A server without statvfs: the driver says so instead of making a number up.
func TestSpace_AServerWithoutStatvfsCannotTell(t *testing.T) {
	s := newFakeSFTP(t)
	s.handlers.FileCmd = plainCmder{s.handlers.FileCmd}
	d := sftpDriver(t, s.addr(), nil)
	if sp, err := d.Space(context.Background(), ""); err == nil {
		t.Errorf("a server without statvfs was measured: %+v", sp)
	}
}
