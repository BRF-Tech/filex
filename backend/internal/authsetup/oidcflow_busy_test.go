package authsetup

import (
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
)

// A full book of sign-ins in flight refuses a new start with a reason the
// sign-in page can say ("try again in a few minutes"), not "SSO failed".
func TestFlowBook_AFullBookIsBusy(t *testing.T) {
	b := &flowBook{flows: map[string]*pendingFlow{}}
	later := time.Now().Add(time.Hour)
	for i := 0; i < maxFlows; i++ {
		b.flows["f"+strconv.Itoa(i)] = &pendingFlow{expires: later}
	}
	require.Len(t, b.flows, maxFlows)
	_, err := b.put(&pendingFlow{expires: later})
	require.Error(t, err)
	assert.Equal(t, auth.SSOReasonBusy, auth.SSOReason(err))
}
