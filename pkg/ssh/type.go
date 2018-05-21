package ssh

import (
	"github.com/onetwotrip/nodeup/pkg/nodeup_const"
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/ssh"
)

type Ssh struct {
	nodeup nodeup.NodeUP
	client *ssh.Client

	log *logrus.Entry
}
