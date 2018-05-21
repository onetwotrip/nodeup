package migrate

import (
	"github.com/onetwotrip/nodeup/pkg/nodeup"
	"github.com/sirupsen/logrus"
)

type Migrate struct {
	nodeup nodeup.NodeUP
	log    *logrus.Entry
}
