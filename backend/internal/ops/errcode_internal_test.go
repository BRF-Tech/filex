package ops

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/brf-tech/filex/backend/internal/apierr"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/trash"
)

// A failed row keeps a code, and the sentence is made when it is read, in the
// reader's language (0.54 audit A2).
//
// RED PROOF (int/054-wave d4107407): the row kept Go's text ("something with
// that name already exists here") and nothing else; the explorer matched it
// with a regular expression to say it, the CLI printed it in English.
func TestFailure_TheRowKeepsTheCodeAndIsSaidWhenRead(t *testing.T) {
	for _, c := range []struct {
		err  error
		code string
	}{
		{fmt.Errorf("rename a: %w", ErrNameTaken), "name_taken"},
		{fmt.Errorf("purge 7: %w", trash.ErrNotInTrash), "not_in_trash"},
		{apierr.New("name_taken", apierr.Params{"name": "a.txt"}, fmt.Errorf("something already exists at this path: a.txt")), "name_taken"},
	} {
		op := &Op{Status: StatusFailed, Error: encodeFailure(c.err, "")}
		decodeFailure(op)
		assert.Equal(t, c.code, op.ErrorCode, "%v", c.err)
		assert.Equal(t, c.err.Error(), op.Error, "the English stays the detail")

		sayErrors(srvtext.WithReader(context.Background(), "tr"), []*Op{op})
		assert.Equal(t, apierr.Text("tr", c.code, op.ErrorParams), op.ErrorText)
		assert.NotEmpty(t, op.ErrorText)
	}

	// A failure with no code keeps its text, as before.
	op := &Op{Status: StatusFailed, Error: encodeFailure(fmt.Errorf("disk on fire"), "2 skipped")}
	decodeFailure(op)
	assert.Equal(t, "", op.ErrorCode)
	assert.Equal(t, "disk on fire; 2 skipped", op.Error)
	sayErrors(context.Background(), []*Op{op})
	assert.Equal(t, "", op.ErrorText)

	// An app job's code (the plugin Decorator's) is said too, with its engine.
	job := &Op{Status: StatusFailed, ErrorCode: "engine_missing", ErrorEngine: "ffmpeg"}
	sayErrors(srvtext.WithReader(context.Background(), "en"), []*Op{job})
	assert.Contains(t, job.ErrorText, "ffmpeg")
}
