#!/usr/bin/env bash

set -o xtrace
set -o errexit
set -o nounset
set -o pipefail

function raymondversionfast()
{
  go mod edit -json | jq -r '.Require[] | select(.Path == "github.com/luthersystems/raymond") | .Version'
}

raymondversionfast

RAYMONDVERSION="$(raymondversionfast)"

(
  echo package libhandlebars
  echo var raymondVersion = '"'"$RAYMONDVERSION"'"'
) >./libhandlebars_version.go
