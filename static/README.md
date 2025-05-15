# Serving Embedded Static Files

This package provides a simple way to serve embedded static content (like HTML, CSS, JS) using the Go `embed` package, integrated with your Oracle service.

---

## 🚀 Quick Start

### 1. Embed the `public/` directory

Add a `public/` folder to your package and include static files.

Embed it in your Go code:

```go
//go:embed public/**
var PublicFS embed.FS
```

---

### 2. Register with Oracle

Register the handler in your `startCmd.Run()` using a single call:

```go
func (r *startCmd) Run() error {
	cfg := svc.DefaultConfig()
	cfg.SetPublicContentHandlerOrPanic(PublicFS, "/v1/public/")

	return oracle.Run(r.ctx, &oracle.Config{
		Config:       *cfg,
		PortalConfig: r.PortalConfig,
	})
}
```

---

### 🔗 Access Embedded Files

Once running, static files are served under the mount prefix. For example:

```
http://localhost:8080/v1/public/index.html
```

Make sure your file exists under the embedded `public/` folder, like:

```
public/index.html
```

---

## 🧠 Notes

- The mount prefix must **start and end with `/`**, e.g. `"/v1/public/"`
- Files are served relative to the `public/` directory
- No additional setup is required — just embed and mount

---
