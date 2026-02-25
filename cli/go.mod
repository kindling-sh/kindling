module github.com/jeffvincent/kindling/cli

go 1.25

require (
	github.com/jeffvincent/kindling/pkg/ci v0.0.0
	github.com/spf13/cobra v1.8.0
)

replace github.com/jeffvincent/kindling/pkg/ci => ../pkg/ci

require (
	github.com/fsnotify/fsnotify v1.9.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	golang.org/x/sys v0.13.0 // indirect
)
