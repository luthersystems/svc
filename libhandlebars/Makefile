PROJECT_REL_DIR=../

.PHONY: raymondversion
raymondversion: libhandlebars_version.go
	@

libhandlebars_version.go: raymondversion.sh ${PROJECT_REL_DIR}/go.mod
	bash ./raymondversion.sh
