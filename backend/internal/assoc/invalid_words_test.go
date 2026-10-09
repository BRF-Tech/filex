package assoc

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A rule refused names the sentence it is said in (ErrInvalid.Say, read by
// handlers/file_types_admin.go as server.error.rule_<Say>) and its values,
// beside the English for a log.
//
// RED PROOF (int/055-wave d8a8cc2f): ErrInvalid carried only its English
// Message, and the Default apps page printed it to every reader.
func TestCheck_ARefusedRuleNamesItsSentence(t *testing.T) {
	s := &Service{}
	var bad *ErrInvalid

	err := s.Check("sideways", "txt", Rule{})
	require.True(t, errors.As(err, &bad), "%v", err)
	assert.Equal(t, "capability", bad.Say)
	assert.Equal(t, "sideways", bad.Params["capability"])
	assert.NotEmpty(t, bad.Message)

	err = s.Check(CapOpen, "BAD.X", Rule{})
	require.True(t, errors.As(err, &bad), "%v", err)
	assert.Equal(t, "kind", bad.Say)
	assert.Equal(t, "BAD.X", bad.Params["ext"])
}

// A File types choice not written says why in a sentence the handlers say in
// the reader's language (PlaceFailure.Say). RED before: PlaceForApp answered
// English strings only, which the wizard printed inside a Turkish notice.
func TestPlaceForAppFailures_NameTheirSentence(t *testing.T) {
	ctx := context.Background()
	s, _ := service(t)
	failed := s.PlaceForAppFailures(ctx, "zeta", []Placement{
		{Capability: CapOpen, Ext: "svg", Handler: "app:zeta/viewer", Place: PlaceOff},
		{Capability: CapOpen, Ext: "drawio", Handler: "app:zeta/viewer", Place: PlaceFirst},
		{Capability: CapOpen, Ext: "drawio", Handler: "app:drawio/editor", Place: PlaceOff},
	}, map[string]bool{KindKey(CapOpen, "svg"): true}, nil)
	require.Len(t, failed, 2, "%v", failed)
	assert.Equal(t, "place_not_new", failed[0].Say)
	assert.Equal(t, "drawio", failed[0].Ext)
	assert.Equal(t, "place_not_own", failed[1].Say)
	assert.Equal(t, "app:drawio/editor", failed[1].Handler)
	assert.NotEmpty(t, failed[1].Message)
}
