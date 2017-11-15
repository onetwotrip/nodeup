package openstack

import (
	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/flavors"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/images"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/extensions/keypairs"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/servers"
	"os"
	"github.com/sirupsen/logrus"
	"github.com/foxdalas/nodeup/pkg/nodeup_const"
	"time"
)


func New(nodeup nodeup.NodeUP, authURL string, tenantName string, username string, password string, key string, keyName string, flavor string) *Openstack {

	o := &Openstack{
		nodeup: nodeup,
		flavorName: flavor,
		key: key,
		keyName: keyName,
	}

	var err error

	opts, err := openstack.AuthOptionsFromEnv()
	if err != nil {
		o.Log().Fatal("AUTH Provide options: %s", err)
	}
	provider, err := openstack.AuthenticatedClient(opts)
	if err != nil {
		o.Log().Fatalf("AUTH Client: %s", err)
	}

	o.client, err = openstack.NewComputeV2(provider, gophercloud.EndpointOpts{
		Region: os.Getenv("OS_REGION_NAME"),
	})
	if err != nil {
		o.Log().Fatalf("Compute: %s", err)
	}
	return o
}

func (o *Openstack) getFlavorByName() string {
	o.Log().Infof("Searching FlavorID for Flavor name: %s", o.flavorName)
	flavorID, err := flavors.IDFromName(o.client, o.flavorName)
	if err != nil {
		o.Log().Fatalf("Flavor: %s", err)
	}
	o.Log().Infof("Found flavor id: %s", flavorID)
	return flavorID
}

func (o *Openstack) getImageByName() string {
	imageID, err := images.IDFromName(o.client, "Ubuntu 16.04-server (64 bit)")
	if err != nil {
		o.Log().Fatalf("Error image: %s", err)
	}
	return imageID
}

func (o *Openstack) createAdminKey() bool {

	//Checking existing keypair
	allPages, err := keypairs.List(o.client).AllPages()
	if err != nil {
		panic(err)
	}

	allKeyPairs, err := keypairs.ExtractKeyPairs(allPages)
	if err != nil {
		panic(err)
	}

	validation := false
	for _, kp := range allKeyPairs {
		if kp.Name == o.keyName {
			o.Log().Infof("Keypair with name %s already exists", o.keyName)
			o.Log().Infof("Checking key data for %s", o.keyName)
			if kp.PublicKey == o.key {
				o.Log().Infof("Keypair with name %s already exists", o.keyName)
				validation = true
			}
		}
	}
	if !validation {
		o.Log().Infof("Keypair with name %s does not exist. Creating...", o.keyName)
		keycreateOpts := keypairs.CreateOpts{
			Name:      o.keyName,
			PublicKey: o.key,
		}

		keypair, err := keypairs.Create(o.client, keycreateOpts).Extract()
		if err != nil {
			o.Log().Fatalf("Keypair %s: %s",o.keyName, err)
			return false
		}
		o.Log().Infof("Keypair %s was created", keypair.Name)
	}

	return true
}



func (o *Openstack) CreateSever(hostname string) *servers.Server {
	flavorID := o.getFlavorByName()
	imageID := o.getImageByName()

	o.Log().Infof("Creating server with flavor ID %s and imageID %s", flavorID, imageID)

	o.createAdminKey()

	serverCreateOpts := servers.CreateOpts{
		Name:      hostname,
		FlavorRef: flavorID,
		ImageRef:  imageID,
		Networks: []servers.Network{
			servers.Network{UUID: "3ad3f99c-bba2-4515-b190-f13258956450"},
		},
	}

	createOpts := keypairs.CreateOptsExt{
		CreateOptsBuilder: serverCreateOpts,
		KeyName:           o.keyName,
	}

	server, err := servers.Create(o.client, createOpts).Extract()
	if err != nil {
		o.Log().Errorf("Error: creating server: %s", err)
	}

	info := o.getServer(server.ID)

	o.Log().Infof("Waiting server %s up", info.Name)
	i := 0
	status := ""
	for {
		time.Sleep(5 * time.Second) //Waiting 5 second before retry getting openstack host status
		info = o.getServer(server.ID)

		if info.Status == status {
			i++
			continue
		}

		if info.Status == "ACTIVE" {
			o.Log().Infof("Server %s status is %s",info.Name, info.Status)
			break
		}
		o.Log().Infof("Server %s status is %s", info.Name, info.Status)
		i++
		if i >= 10 {
			o.Log().Errorf("Timeout for server %s with status %s", info.Name, info.Status)
		}
	}
	return info
}

func (o *Openstack) getServer(sid string) *servers.Server {
	server, _ := servers.Get(o.client, sid).Extract()
	return server
}

func (o *Openstack) Log() *logrus.Entry {
	log := o.nodeup.Log().WithField("context", "openstack")
	return log
}

func (o *Openstack) DeleteServer(sid string) {
	o.Log().Infof("Deleting server with ID %s", sid)
	result :=  servers.Delete(o.client, sid)
	if result.Err != nil {
		o.Log().Errorf("Deleting error: %s", result.Err)
	} else {
		o.Log().Infof("Server %s deleted", sid)
	}
}
