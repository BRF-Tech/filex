package thumb

// MakeClip exposes the ffmpeg fixture builder to the external test package, so
// a test there makes its clip in a temporary directory instead of reading one
// from outside the Go module (the release tool mirrors backend/ alone).
var MakeClip = makeClip
