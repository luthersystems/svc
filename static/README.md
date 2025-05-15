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
handler, err := static.PublicHandler(PublicFS, "/v1/public/")
```

This will serve files under the `/v1/public/` URL path.

---

### Optional: Panic wrapper

For convenience, create a panic-on-failure helper in your app:

```go
func PublicContentHandlerOrPanic(prefix string) http.Handler {
	if h, err := static.PublicHandler(publicFS, prefix); err != nil {
		panic(err)
	} else {
		return h
	}
}
```

---

### 3. Register the handler with Oracle

Pass the handler to `SetPublicContentHandler` method of the Oracle:

```go
cfg.SetPublicContentHandler(api.PublicContentHandlerOrPanic("/v1/public/"),"/v1/public/")
```

### 4. Access the files in the browser to verify successful setup

Visit:  
```
<oracle_base_url>/v1//public/<path_to_file>
```

---

## Example Usage

```go
func (r *startCmd) Run() error {
	const publicPathPrefix = "/v1/public/"

	cfg := svc.DefaultConfig()
	// ...
	cfg.SetPublicContentHandler(api.PublicContentHandlerOrPanic(publicPathPrefix), publicPathPrefix)
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

func PublicContentHandlerOrPanic(prefix string) http.Handler {
	if h, err := static.PublicHandler(publicFS, prefix); err != nil {
		panic(err)
	} else {
		return h
	}
}
```
