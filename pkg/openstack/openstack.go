package openstack

import (
	"encoding/json"
	"errors"
	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/extensions/hypervisors"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/extensions/keypairs"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/extensions/migrate"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/extensions/networks"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/extensions/schedulerhints"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/extensions/startstop"

	util_flavors "github.com/gophercloud/utils/openstack/compute/v2/flavors"
	"github.com/gophercloud/utils/openstack/imageservice/v2/images"
	util_servers "github.com/gophercloud/utils/openstack/compute/v2/servers"

	"github.com/onetwotrip/nodeup/pkg/nodeup_const"

	"github.com/gophercloud/gophercloud/openstack/compute/v2/flavors"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/servers"

	"github.com/patrickmn/go-cache"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

func New(nodeup nodeup.NodeUP, key string, keyName string, flavor string, image string) *Openstack {

	o := &Openstack{
		nodeup:     nodeup,
		flavorName: flavor,
		imageName:  image,
		key:        key,
		keyName:    keyName,
		cache:      cache.New(5*time.Minute, 10*time.Minute),
	}

	var err error

	opts, err := openstack.AuthOptionsFromEnv()
	o.assertError(err, "AUTH Provide options")

	provider, err := openstack.AuthenticatedClient(opts)
	o.assertError(err, "AUTH Client")

	o.client, err = openstack.NewIdentityV3(provider, gophercloud.EndpointOpts{
		Region: os.Getenv("OS_REGION_NAME"),
	})
	o.assertError(err, "IDENTITY_v3")

	o.client, err = openstack.NewComputeV2(provider, gophercloud.EndpointOpts{
		Region: os.Getenv("OS_REGION_NAME"),
	})
	o.assertError(err, "COMPUTE")

	return o
}

func (o *Openstack) getFlavorByName() string {
	o.Log().Debugf("Searching FlavorID for Flavor name: %s", o.flavorName)
	flavorID, err := util_flavors.IDFromName(o.client, o.flavorName)
	o.assertError(err, "Flavor")

	o.Log().Debugf("Found flavor id: %s", flavorID)
	return flavorID
}

func (o *Openstack) getImageByName() string {
	o.Log().Debugf("Searching ImageID for image: %s", o.imageName)
	imageID, err := images.IDFromName(o.client, o.imageName)
	o.assertError(err, "Error image")

	o.Log().Debugf("Found image id: %s", imageID)
	return imageID
}

func (o *Openstack) getNetworkIDs(defineNetworks string) ([]string, error) {
	//Servers.com networking labels
	//External - internet_XX.XX.XX.XX/XX
	//Internal - local_private
	//Global Internal - global_private

	var networksID []string

	allPages, err := networks.List(o.client).AllPages()
	if err != nil {
		o.Log().Errorf("List networks: %s", err)
		return networksID, err
	}
	allNetworks, err := networks.ExtractNetworks(allPages)
	if err != nil {
		o.Log().Errorf("Extract networks: %s", err)
		return networksID, err
	}
	if len(defineNetworks) > 0 {
		for _, net := range allNetworks {
			for _, selected := range strings.Split(defineNetworks, ",") {
				if selected == net.Label {
					networksID = append(networksID, net.ID)
				}
			}
		}
	} else {
		o.Log().Error("Please provide networks")
		return networksID, errors.New("Networks list not found")
	}
	return networksID, err
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
			o.Log().Debugf("Keypair with name %s already exists", o.keyName)
			o.Log().Debugf("Checking key data for %s", o.keyName)
			if kp.PublicKey == string(o.key) {
				o.Log().Debugf("Keypair with name %s already exists", o.keyName)
				validation = true
			} else {
				o.Log().Debugf("Deleting keypair with name %s", o.keyName)
				err := keypairs.Delete(o.client, o.keyName).ExtractErr()
				if err != nil {
					o.Log().Errorf("Can't delete keypair with name %s", o.keyName)
				}
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
			o.Log().Fatalf("Keypair %s: %s", o.keyName, err)
			return false
		}
		o.Log().Debugf("Keypair %s was created", keypair.Name)
	}

	return true
}

func (o *Openstack) CreateServer(hostname string, timeout int, group string, networks string, availabilityZone string) (*servers.Server, error) {

	if o.isServerExist(hostname) {
		o.Log().Fatalf("Server %s already exists", hostname)
	}

	flavorID := o.getFlavorByName()
	imageID := o.getImageByName()
	networksIDs, err := o.getNetworkIDs(networks)
	if err != nil {
		o.Log().Errorf("Error networks: %s", err)
		return nil, err
	}

	o.Log().Infof("Creating server with hostname %s", hostname)

	o.createAdminKey()

	var s []servers.Network

	for _, n := range networksIDs {
		s = append(s, servers.Network{UUID: n})
	}

	configDrive := true

	serverCreateOpts := servers.CreateOpts{
		Name:        hostname,
		FlavorRef:   flavorID,
		ImageRef:    imageID,
		Networks:    s,
		ConfigDrive: &configDrive,
	}

	// TODO: add auto balancer
	if len(availabilityZone) > 0 {
		o.Log().Infof("Launching server in availability zone %s", availabilityZone)
		serverCreateOpts.AvailabilityZone = availabilityZone
	}

	createOpts := keypairs.CreateOptsExt{
		CreateOptsBuilder: serverCreateOpts,
		KeyName:           o.keyName,
	}

	var server *servers.Server

	if len(group) > 5 {
		server, err = servers.Create(o.client, schedulerhints.CreateOptsExt{
			CreateOptsBuilder: createOpts,
			SchedulerHints: schedulerhints.SchedulerHints{
				Group: group,
			},
		}).Extract()
		if err != nil {
			o.Log().Errorf("Error: creating server: %s", err)
			return nil, err
		}
	} else {
		server, err = servers.Create(o.client, createOpts).Extract()
		if err != nil {
			o.Log().Errorf("Error: creating server: %s", err)
			return nil, err
		}
	}

	info, err := o.GetServer(server.ID)
	if err != nil {
		o.Log().Error(err)
	}

	o.Log().Debugf("Waiting server %s up", info.Name)
	i := 0
	status := ""
	for {
		time.Sleep(time.Duration(timeout)*time.Second) //Waiting before retry getting openstack host status
		info, err = o.GetServer(server.ID)
		if err != nil {
			o.Log().Error(err)
		}

		if info.Status == status {
			i++
			continue
		}

		if info.Status == "ACTIVE" {
			o.Log().Infof("Server %s status is %s", info.Name, info.Status)
			break
		}
		if info.Status == "ERROR" {
			o.Log().Errorf("Bootstrap error: %s", info.Name)
			o.Log().Errorf("Status: %s", info.Status)
			o.Log().Errorf("Fault message: %s", info.Fault.Message)
			o.Log().Errorf("Fault code: %d", info.Fault.Code)
			o.DeleteServer(server.ID)
			return info, errors.New(info.Fault.Message)
		}
		o.Log().Debugf("Server %s status is %s", info.Name, info.Status)
		i++
		if i >= 10 {
			o.Log().Errorf("Timeout for server %s with status %s", info.Name, info.Status)
			o.Log().Errorf("Fault: %s", info.Fault.Message)
			return info, errors.New("timeout")
		}
	}
	return info, nil
}

func (o *Openstack) GetServer(sid string) (*servers.Server, error) {
	server, err := servers.Get(o.client, sid).Extract()
	if err != nil {
		o.Log().Error(err)
		return nil, err
	}
	return server, nil
}

func (o *Openstack) GetServerDetail(sid string) (Server, error) {
	var server Server
	err := servers.Get(o.client, sid).ExtractInto(&server)
	if err != nil {
		o.Log().Error(err)
		return server, err
	}
	return server, nil
}

func (o *Openstack) GetHypervisors() ([]hypervisors.Hypervisor, error) {
	allPages, err := hypervisors.List(o.client).AllPages()
	if err != nil {
		o.Log().Error(err)
		return nil, err
	}
	allHypervisors, err := hypervisors.ExtractHypervisors(allPages)
	if err != nil {
		o.Log().Error(err)
		return nil, err
	}
	return allHypervisors, nil
}

func (o *Openstack) GetHypervisorInfo(id string) (*hypervisors.Hypervisor, error) {
	hypervisor, err := hypervisors.Get(o.client, id).Extract()
	if err != nil {
		o.Log().Error(err)
		return nil, err
	}

	return hypervisor, nil
}

func (o *Openstack) GetHypervisorStatistics(id int) (*hypervisors.Statistics, error) {
	hypervisorsStatistics, err := hypervisors.GetStatistics(o.client).Extract()
	if err != nil {
		o.Log().Error(err)
		return nil, err
	}
	return hypervisorsStatistics, nil
}

func (o *Openstack) GetServers() ([]servers.Server, error) {
	opts := servers.ListOpts{}
	allPages, err := servers.List(o.client, opts).AllPages()
	if err != nil {
		o.Log().Error(err)
		return nil, err
	}
	allServers, err := servers.ExtractServers(allPages)
	if err != nil {
		o.Log().Error(err)
		return nil, err
	}
	return allServers, nil
}

func (o *Openstack) GetFlavors() ([]flavors.Flavor, error) {
	listOpts := flavors.ListOpts{
		AccessType: flavors.PrivateAccess,
	}
	allPages, err := flavors.ListDetail(o.client, listOpts).AllPages()
	if err != nil {
		panic(err)
	}

	allFlavors, err := flavors.ExtractFlavors(allPages)
	if err != nil {
		o.Log().Error(err)
		return nil, err
	}

	return allFlavors, nil
}

func (o *Openstack) GetFlavorInfo(id string) (*flavors.Flavor, error) {
	flavor, err := flavors.Get(o.client, id).Extract()
	if err != nil {
		o.Log().Error(err)
		return nil, err
	}

	return flavor, nil
}

func (o *Openstack) StartServer(id string) error {
	return startstop.Start(o.client, id).ExtractErr()
}

func (o *Openstack) StopServer(id string) error {
	return startstop.Stop(o.client, id).ExtractErr()
}

// Criteria
// cpu - CPU Sensitive
// memory - Memory Sensitive by Free RAM metric
func (o *Openstack) HypervisorScheduler(criteria string) []hypervisors.Hypervisor {

	hypervisors, err := o.GetHypervisors()
	if err != nil {
		o.Log().Fatal(err)
	}
	switch criteria {
	case "cpu":
		sort.Sort(sortedHypervisorsByvCPU(hypervisors))
	case "memory":
		sort.Sort(sortedHypervisorsBMemory(hypervisors))
	default:
		return hypervisors
	}
	return hypervisors
}

func (o *Openstack) GetHypervisorWithSensitiveCriteria(criteria string) hypervisors.Hypervisor {
	var h hypervisors.Hypervisor
	for _, hypervisor := range o.HypervisorScheduler(criteria) {
		if hypervisor.Status == "enabled" && hypervisor.State == "up" {
			h = hypervisor
		}
		continue
	}
	return h
}

func (o *Openstack) isServerExist(name string) bool {
	_, err := util_servers.IDFromName(o.client, name)
	if err != nil {
		o.Log().Debug(err)
		return false
	} else {
		o.Log().Infof("Server with name %s already exist", name)
		return true
	}
}

func (o *Openstack) DeleteServer(sid string) error {
	o.Log().Infof("Deleting server with ID %s", sid)
	result := servers.Delete(o.client, sid)
	if result.Err != nil {
		o.Log().Errorf("Deleting error: %s", result.Err)
	} else {
		o.Log().Infof("Server %s deleted", sid)
	}
	return result.Err
}

func (o *Openstack) DeleteIfError(id string, err error) bool {
	if err != nil {
		o.Log().Error(err)
		err = o.DeleteServer(id)
		if err != nil {
			o.Log().Errorf("Openstack host delete error %s", err)
		}
		return true
	} else {
		return false
	}
}

func (o *Openstack) IDFromName(hostname string) (string, error) {
	count := 0
	id := ""
	var servers []servers.Server
	var err error

	cache, found := o.cache.Get("servers")
	if found {
		err = json.Unmarshal(cache.([]byte), &servers)
		if err != nil {
			return "", err
		}
	} else {
		servers, err = o.GetServers()
		if err != nil {
			return "", err
		}
		json, err := json.Marshal(servers)
		if err != nil {
			return "", err
		}
		o.cache.Set("servers", json, 10*time.Minute)
	}

	for _, f := range servers {
		if f.Name == hostname {
			count++
			id = f.ID
		}
	}

	switch count {
	case 0:
		return "", gophercloud.ErrResourceNotFound{Name: hostname, ResourceType: "server"}
	case 1:
		return id, nil
	default:
		return "", gophercloud.ErrMultipleResourcesFound{Name: hostname, Count: count, ResourceType: "server"}
	}
}

func (o *Openstack) Migrate(serverID string, hypervisorName string, blockMigration bool, diskOverCommit bool) error {
	migrationOpts := migrate.LiveMigrateOpts{
		Host:           &hypervisorName,
		BlockMigration: &blockMigration,
		DiskOverCommit: &diskOverCommit,
	}

	err := migrate.LiveMigrate(o.client, serverID, migrationOpts).ExtractErr()
	return err
}

func (o *Openstack) MigrateHost(id string, hypervisor string, wg *sync.WaitGroup) bool {
	defer wg.Done()

	serverInfo, err := o.GetServer(id)
	if err != nil {
		o.Log().Error(err)
	}
	if serverInfo.Status == "MIGRATING" {
		o.Log().Errorf("Server %s already in migration state", id)
		return false
	}

	o.Log().Infof("Migration process to hypervisor %s started for hostID %s", hypervisor, id)

	err = o.Migrate(id, hypervisor, true, false)
	if err != nil {
		o.Log().Error(err)
		return false
	}

	doneCh := make(chan bool, 1)
	resChan := make(chan bool)
	go func(doneCh, resCh chan bool) {
		ticker := time.NewTicker(10 * time.Second)

		for {
			select {
			case <-ticker.C:
				serverInfo, err := o.GetServer(id)
				if err != nil {
					o.Log().Error(err)
				}

				if serverInfo.Status == "MIGRATING" {
					o.Log().Infof("Server %s is still migrating", serverInfo.Name)
					continue
				}
				if serverInfo.Status == "ACTIVE" {
					o.Log().Infof("Server %s migration process is done", serverInfo.Name)
					resCh <- true
					return
				}
			case <-doneCh:
				return
			}
		}
	}(doneCh, resChan)

	timer := time.NewTimer(time.Hour)

	select {
	case <-timer.C:
		doneCh <- true
		return false
	case res := <-resChan:
		return res

	}
}
