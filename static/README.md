# static

A minimal helper package for serving embedded static files in Go.

This package provides two utilities:

- `StaticContentHandlerOrPanic`: A flexible function for serving files embedded under any subdirectory.
- `StaticHandlerOrPanic`: An opinionated variant that assumes files are embedded under `static/**` and always serves them from `/static/`.

---

## 🧩 Usage

### 1. Embed your static files

Use `go:embed` to embed static assets into your binary:

```go
//go:embed static/**
var staticFiles embed.FS
```

Ensure the relative path to the files is `/static/`

### 2. Add to Oracle config

```go
func (r *startCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		log.Fatalf("could not get working directory: %v", err)
	}
	log.Printf("Process running from: %s", dir)
	cfg := svc.DefaultConfig()
	// ...
	cfg.AddStaticHandler("/static/", static.StaticHandlerOrPanic(api.EmbeddedFiles))
    // ...

	return oracle.Run(r.ctx, &oracle.Config{
		Config:       *cfg,
		PortalConfig: r.PortalConfig,
	})
}
```

The files should be served at `/static/<fileName>