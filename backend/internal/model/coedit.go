package model

// Editing together (migration 00107, internal/coedit, task #189): the
// relay's rows. Times are Unix milliseconds.

// CoEditSession is one file being edited together.
type CoEditSession struct {
	SID       string
	DocKey    string
	StorageID int64
	Path      string
	App       string
	Build     string
	BaseSig   string
	SavedSig  string
	// KeyMode is "server" (the server made the key and keeps it sealed with
	// its secret key) or "folder" (the members' browsers sealed it with the
	// folder key; the server cannot open it).
	KeyMode      string
	SealedKey    string
	UserSalt     string
	Rev          int64
	Head         int64
	ChangesHead  int64
	SavedThrough int64
	LogBytes     int64
	BlobBytes    int64
	HasBase      bool
	Members      int
	NextClient   int64
	NextIndex    int64
	LeaseHolder  string
	LeaseUntilMs int64
	CreatedMs    int64
	ActiveMs     int64
}

// CoEditMember is one connection in a session.
type CoEditMember struct {
	SID    string
	Client string
	// TokenHash is hex SHA-256 of the member's token (never the token).
	TokenHash string
	UserID    int64
	UserRef   string
	Name      string
	IndexUser int64
	CanEdit   bool
	JoinSeq   int64
	LastCtr   int64
	SeenMs    int64
}

// CoEditEntry is one place in a session's log. CT is a member's sealed
// payload; Member (JSON), Through and Sig are the relay's own metadata.
type CoEditEntry struct {
	SID     string
	Seq     int64
	Kind    string
	Client  string
	AtMs    int64
	Ctr     int64
	CT      []byte
	Member  string
	Through int64
	Sig     string
}

// CoEditBlob is a stored sealed blob of a session (its bytes are a file).
type CoEditBlob struct {
	SID       string
	Name      string
	Size      int64
	SHA256    string
	CreatedMs int64
}

// CoEditCursor is a member's latest sealed cursor (migration 00109): how a
// member connected to another filex process on the same database gets it.
type CoEditCursor struct {
	SID    string
	Client string
	Ctr    int64
	CT     []byte
	AtMs   int64
}
