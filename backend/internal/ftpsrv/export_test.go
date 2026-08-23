package ftpsrv

// GenerateSelfSigned exposes the self-signed generator to the external test
// package, which replaces the certificate on disk to prove it is re-read.
var GenerateSelfSigned = generateSelfSigned
