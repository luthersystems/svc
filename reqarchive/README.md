# reqarchive package

The reqarchive package implements HTTP middleware (see `midware` package) for
use in archiving HTTP requests to a service for debugging purposes.  The handler
stores the request URL path, query options, method, body and JWT claims in a
JSON document.  Requests must have a trace header (request ID) defined.  This
can be implemented with `midware.TraceHeaders`.

This package currently provider an implementation of the archiver middleware
backed by AWS S3 which stores requests in a bucket keyed by request id under a
prefix.

## example usage

Given an HTTP handler, the below code wraps that with a request archiver:
```
func Listen(mainHandler http.Handler) error {
	archiver, err := NewS3Archiver("aws-region", "s3-bucket", "prefix")
	if err != nil {
		return err
	}
	middleware := midware.Chain{
		midware.TraceHeaders("", true),
		archiver,
	}
	handler := middleware.Wrap(mainHandler)
	return http.ListenAndServe(":8080", handler)
}
```

## recommended S3 Bucket configuration

A separate S3 bucket should be configured with KMS encryption and a lifecycle
rule to delete objects afer a short amount of time (7 days or so).
