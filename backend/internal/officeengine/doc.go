// Package officeengine runs an app's office engine on this server: the
// Office editor app's x2t (ONLYOFFICE's converter) and its page layout
// (sdkjs in QuickJS), both compiled to WebAssembly and shipped in the app's
// release as engine.zip (docs/ONLYOFFICE.md → The office engine without a
// Document Server, docs/APP-PLUGINS-API.md → engines.office).
//
// It is the second back end of the office engine apps convert with
// (`engines:office`) and of office thumbnails, beside the ONLYOFFICE
// Document Server (internal/onlyoffice). The server picks one of them
// (internal/server, the `engine` capability of Default apps); this package
// only runs the app's.
//
// # Two sides, one binary
//
// filex never links the engine's code (AGPL): it runs it as a separate
// WebAssembly program, files in and files out, the way it would run a
// Document Server's x2t. The PARENT side (Runner, in the server) starts
// `filex office-engine` - a hidden command of this same binary, the CHILD
// side (Serve) - and talks to it over the child's stdin and stdout, one JSON
// line per job (Request) and one per answer (Answer). The child holds the
// wazero runtimes: x2t-server.wasm, an instance per job, mounted on a
// directory of its own; and a warm layout worker per kind of document
// (layout-host.wasm running the app's worker.js over sdkjs).
//
// # Why a process of its own
//
//   - Speed: both modules are compiled WITHOUT wazero's context watch
//     (2-4.6 times faster on x2t, measured), and such a module can only be
//     stopped by ending its process. The parent keeps every job's deadline
//     and kills the process when it passes; the next job starts another.
//   - Memory: x2t takes 284 MiB at the least and 2.6 GiB for a 200,000-row
//     csv. In a process of its own that memory, a trap and a loop go back to
//     the system with the process, never into filex's heap.
//
// # Memory
//
// Every instance is capped (FILEX_OFFICE_ENGINE_MAX_MB, MaxMBDefault): its
// memory may not grow past the cap. x2t's memory is reserved up to the cap
// when the instance starts (memory.go), so growing it never copies it - the
// 200,000-row csv peaked at 6.2 GB of process with copies, 2.6 GiB without.
// The reservation is address space outside Go's heap: the system backs only
// what x2t writes and takes it all back when the instance closes, so job
// after job the process holds what the job used, not the cap.
// The parent also watches the process as a whole where the system lets it
// read a process's resident memory (proc_*.go): about twice the cap ends it.
// A layout worker is started again after WorkerJobs jobs or WorkerMaxBytes of
// memory; a process left idle for Options.IdleTimeout ends.
//
// # Compiling, and "preparing"
//
// wazero compiles x2t-server.wasm in 3.5-5 minutes the first time on a
// machine; its cache (in filex's data directory) loads it in about a second
// in every process after that. So a new engine package - an install, an
// upgrade, a new filex - is PREPARED in the background by one process that
// compiles both modules and starts a worker of each kind once (which writes
// the layout's bytecode); until it is done the engine is not Ready, its
// Status says "preparing", and a Document Server, if one is connected, takes
// the jobs meanwhile.
package officeengine
