package api

// tcpNotSentLowat is macOS's TCP_NOTSENT_LOWAT, which the syscall package
// does not name on every architecture.
const tcpNotSentLowat = 0x201
