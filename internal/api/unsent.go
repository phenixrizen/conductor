package api

// hostedViewerUnsent is the most a hosted viewer's socket holds that it has
// not sent yet, on Linux, which can bound it (limitUnsent): what waits
// for a viewer that reads slowly, or not at all, then waits in its queue,
// where the server counts it and tells the host the viewer is behind
// (signal's viewer_behind), and not in the socket's buffer, which grows to
// megabytes on its own. It does not bound what is in flight to a viewer
// that reads, so it slows none.
const hostedViewerUnsent = 128 << 10
