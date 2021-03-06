package ssh

import (
	"github.com/onetwotrip/nodeup/pkg/nodeup_const"
	"github.com/pkg/sftp"
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"io"
	"net"
	"os"
	"os/exec"
)

func New(nodeup nodeup.NodeUP, address string, user string) (*Ssh, error) {
	s := &Ssh{
		nodeup: nodeup,
		client: nil,
	}

	if len(os.Getenv("SSH_AUTH_SOCK")) == 0 {
		cmd := exec.Command("ssh-agent")
		err := cmd.Run()
		if err != nil {
			return s, err
		}
	}

	socket := os.Getenv("SSH_AUTH_SOCK")

	conn, err := net.Dial("unix", socket)
	if err != nil {
		return s, err
	}
	s.Log().Debugf("Using SSH Agent with socket %s", socket)

	agentClient := agent.NewClient(conn)

	sshConfig := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeysCallback(agentClient.Signers),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	client, err := ssh.Dial("tcp", address+":22", sshConfig)
	if err != nil {
		return s, err
	}

	s.client = client

	return s, err
}

func (o *Ssh) Log() *logrus.Entry {
	log := o.nodeup.Log().WithField("context", "ssh")
	return log
}

func (s *Ssh) sshSession() (*ssh.Session, error) {
	session, err := s.client.NewSession()
	if err != nil {
		return session, err
	}
	return session, err
}

func (s *Ssh) RunCommandPipe(command string, outfile *os.File) error {

	session, err := s.sshSession()
	if err != nil {
		s.Log().Errorf("session error: %s", err)
	}
	defer session.Close()

	s.Log().Debugf("Writing bootstrap output to file %s", outfile.Name())

	session.Stdout = io.MultiWriter(outfile)
	session.Stderr = session.Stdout

	s.Log().Debugf("Running %s", command)
	if err := session.Run(command); err != nil {
		s.Log().Errorf("\"%s\" error: %s", command, err)
		return err
	}

	s.Log().Debugf("Finished %s", command)

	return nil
}

func (s *Ssh) TransferFile(data []byte, name string, path string) error {
	s.Log().Debugf("Starting transferring file %s", path+"/"+name)

	sftp, err := sftp.NewClient(s.client)
	s.assertError(err)

	f, err := sftp.Create(name)
	s.assertError(err)

	_, err = f.Write(data)
	s.assertError(err)

	_, err = sftp.Lstat(name)
	s.assertError(err)

	s.Log().Debugf("Finished transferring file %s", path+"/"+name)

	return err
}
