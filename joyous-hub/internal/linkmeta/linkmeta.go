package linkmeta

// Version is injected at link time:
// go build -ldflags "-X joyous-hub/internal/linkmeta.Version=..."
var Version = "dev"
