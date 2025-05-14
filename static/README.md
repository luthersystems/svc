# static

Serve embedded static files from the Oracle using a simple convention.
Designed for use in conjunction with the `oracle` package.

## Usage

### 1. Embed your public directory

Create a folder named `public` inside the package from which you configure your Oracle.
Add any public files you wish to serve.

Use the `embed` package to include them in your Go binary:

```go
//go:embed public/**
var PublicFS embed.FS
```

### 2. Mount it using the provided handler

Use `PublicHandler` to create an `http.Handler` for your embedded files:

```go
handler, err := static.PublicHandler(PublicFS)
```

This will serve files under the `/public/` URL path.

---

### Optional: Panic wrapper

For convenience, create a panic-on-failure helper in your app:

```go
func PublicHandlerOrPanic(fs embed.FS) http.Handler {
    h, err := static.PublicHandler(fs)
    if err != nil {
        panic(err)
    }
    return h
}
```

---

### 3. Register the handler with Oracle

Pass the handler to `SetPublicContentHandler` method of the Oracle:

```go
cfg.SetPublicContentHandler(api.PublicContentHandlerOrPanic())
```

### 4. Access the files in the browser to verify successful setup

Visit:  
```
<oracle_base_url>/public/<path_to_file>
```

---

## Example Usage

```go
func (r *startCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		log.Fatalf("could not get working directory: %v", err)
	}
	log.Printf("Process running from: %s", dir)

	cfg := svc.DefaultConfig()
	// ...
	cfg.SetPublicContentHandler(api.PublicContentHandlerOrPanic())
	// ...
	return oracle.Run(r.ctx, &oracle.Config{
		Config:       *cfg,
		PortalConfig: r.PortalConfig,
	})
}
```

With:

```go
//go:embed public/**
var publicFS embed.FS

func PublicContentHandlerOrPanic() http.Handler {
	h, err := static.PublicHandler(publicFS)
	if err != nil {
		panic(err)
	}
	return h
}
```
