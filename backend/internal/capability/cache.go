package capability

// cache.go — Currently the cache is collocated with Service in service.go
// (sync.RWMutex + cached + until). This file exists for the inevitable
// moment we want to migrate to a more sophisticated cache layer (TTL +
// negative caching + pub/sub invalidation across replicas).
