package sys

import _ "embed"

//go:embed argoz.service
var systemdServiceTemplate string

//go:embed argoz.openrc
var openrcServiceTemplate string
