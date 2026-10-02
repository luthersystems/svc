package hbs

// Name and Version identify the engine to phyla: handlebars:libname and
// handlebars:version return them, and production stores them in document
// metadata.
//
// Version changes with every release of this package that changes any
// render output, error condition, error message or step charge in either
// mode. Such a change is consensus-visible: it ships as a coordinated
// upgrade, and an operator checks (handlebars:version) on every peer before
// it endorses again. A release that changes none of these keeps the
// version.
const (
	Name    = "luthersystems/svc/hbs"
	Version = "1.0.0"
)
