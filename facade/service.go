// Copyright 2014 The Serviced Authors.
// Use of f source code is governed by a

package facade

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zenoss/glog"

	"github.com/control-center/serviced/commons"
	"github.com/control-center/serviced/dao"
	"github.com/control-center/serviced/datastore"
	"github.com/control-center/serviced/domain/addressassignment"
	"github.com/control-center/serviced/domain/host"
	"github.com/control-center/serviced/domain/pool"
	"github.com/control-center/serviced/domain/service"
	"github.com/control-center/serviced/domain/serviceconfigfile"
	"github.com/control-center/serviced/domain/servicedefinition"
	"github.com/control-center/serviced/domain/servicestate"
	"github.com/control-center/serviced/zzk"
	zkregistry "github.com/control-center/serviced/zzk/registry"
	zkscheduler "github.com/control-center/serviced/zzk/scheduler"
	zkservice "github.com/control-center/serviced/zzk/service"
	zkvirtualip "github.com/control-center/serviced/zzk/virtualips"
)

var zkAPI func(f *Facade) zkfuncs = getZKAPI

// AddService adds a service; return error if service already exists
func (f *Facade) AddService(ctx datastore.Context, svc service.Service) error {
	glog.V(2).Infof("Facade.AddService: %+v", svc)
	store := f.serviceStore

	_, err := store.Get(ctx, svc.ID)
	if err != nil && !datastore.IsErrNoSuchEntity(err) {
		return err
	} else if err == nil {
		return fmt.Errorf("error adding service; %v already exists", svc.ID)
	}

	svcCopy := svc
	err = store.Put(ctx, &svc)
	if err != nil {
		glog.V(2).Infof("Facade.AddService: %+v", err)
		return err
	}

	glog.V(2).Infof("Facade.AddService: id %+v", svc.ID)
	if svcCopy.ConfigFiles != nil {
		for key, confFile := range svcCopy.ConfigFiles {
			glog.V(2).Infof("Facade.AddService: calling updateService for %s due to OriginalConfigs of %+v", svcCopy.Name, key)
			confFile.Commit = "initial revision"
			svcCopy.ConfigFiles[key] = confFile
		}
		return f.updateService(ctx, &svcCopy)
	}

	glog.V(2).Infof("Facade.AddService: calling zk.updateService for %s %d ConfigFiles", svc.Name, len(svc.ConfigFiles))
	return zkAPI(f).updateService(&svc)
}

//
func (f *Facade) UpdateService(ctx datastore.Context, svc service.Service) error {
	glog.V(2).Infof("Facade.UpdateService: %+v", svc)
	//cannot update service without validating it.
	if svc.DesiredState == service.SVCRun {
		if err := f.validateServicesForStarting(ctx, &svc); err != nil {
			return err
		}

		for _, ep := range svc.GetServiceVHosts() {
			for _, vh := range ep.VHosts {
				//check that vhosts aren't already started elsewhere
				if err := zkAPI(f).CheckRunningVHost(vh, svc.ID); err != nil {
					return err
				}
			}
		}
	}
	return f.updateService(ctx, &svc)
}

//
func (f *Facade) RemoveService(ctx datastore.Context, id string) error {
	//TODO: should services already be stopped before removing to prevent half running service in case of error while deleting?

	err := f.walkServices(ctx, id, func(svc *service.Service) error {
		zkAPI(f).removeService(svc)
		return nil
	})

	if err != nil {
		//TODO: should we put them back?
		return err
	}

	store := f.serviceStore

	err = f.walkServices(ctx, id, func(svc *service.Service) error {
		err := store.Delete(ctx, svc.ID)
		if err != nil {
			glog.Errorf("Error removing service %s	 %s ", svc.ID, err)
		}
		return err
	})
	if err != nil {
		return err
	}
	//TODO: remove AddressAssignments with f Service
	return nil
}

func (f *Facade) GetService(ctx datastore.Context, id string) (*service.Service, error) {
	glog.V(3).Infof("Facade.GetService: id=%s", id)
	store := f.serviceStore
	svc, err := store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err = f.fillOutService(ctx, svc); err != nil {
		return nil, err
	}
	glog.V(3).Infof("Facade.GetService: id=%s, service=%+v, err=%s", id, svc, err)
	return svc, nil
}

//
func (f *Facade) GetServices(ctx datastore.Context) ([]*service.Service, error) {
	glog.V(3).Infof("Facade.GetServices")
	store := f.serviceStore
	results, err := store.GetServices(ctx)
	if err != nil {
		glog.Error("Facade.GetServices: err=", err)
		return results, err
	}
	if err = f.fillOutServices(ctx, results); err != nil {
		return results, err
	}
	return results, nil
}

//
func (f *Facade) GetTaggedServices(ctx datastore.Context, request dao.EntityRequest) ([]*service.Service, error) {
	glog.V(3).Infof("Facade.GetTaggedServices")

	store := f.serviceStore
	switch v := request.(type) {
	case []string:
		results, err := store.GetTaggedServices(ctx, v...)
		if err != nil {
			glog.Error("Facade.GetTaggedServices: err=", err)
			return results, err
		}
		if err = f.fillOutServices(ctx, results); err != nil {
			return results, err
		}
		glog.V(2).Infof("Facade.GetTaggedServices: services=%v", results)
		return results, nil
	default:
		err := fmt.Errorf("Bad request type: %v", v)
		glog.V(2).Info("Facade.GetTaggedServices: err=", err)
		return []*service.Service{}, err
	}
}

// The tenant id is the root service uuid. Walk the service tree to root to find the tenant id.
func (f *Facade) GetTenantID(ctx datastore.Context, serviceID string) (string, error) {
	glog.V(2).Infof("Facade.GetTenantId: %s", serviceID)
	gs := func(id string) (service.Service, error) {
		return f.getService(ctx, id)
	}
	return getTenantID(serviceID, gs)
}

// Get a service endpoint.
func (f *Facade) GetServiceEndpoints(ctx datastore.Context, serviceId string) (map[string][]*dao.ApplicationEndpoint, error) {
	glog.V(2).Infof("Facade.GetServiceEndpoints serviceId=%s", serviceId)
	result := make(map[string][]*dao.ApplicationEndpoint)
	myService, err := f.getService(ctx, serviceId)
	if err != nil {
		glog.V(2).Infof("Facade.GetServiceEndpoints service=%+v err=%s", myService, err)
		return result, err
	}

	service_imports := myService.GetServiceImports()
	if len(service_imports) > 0 {
		glog.V(2).Infof("%+v service imports=%+v", myService, service_imports)

		servicesList, err := f.getServices(ctx)
		if err != nil {
			return result, err
		}

		// Map all services by Id so we can construct a tree for the current service ID
		glog.V(2).Infof("ServicesList: %d", len(servicesList))
		topService := f.getServiceTree(serviceId, &servicesList)
		// We should now have the top-level service for the current service ID

		//build 'OR' query to grab all service states with in "service" tree
		relatedServiceIDs := walkTree(topService)
		var states []*servicestate.ServiceState
		err = zkAPI(f).getSvcStates(myService.PoolID, &states, relatedServiceIDs...)
		if err != nil {
			return result, err
		}

		//delay getting addresses as long as possible
		f.fillServiceAddr(ctx, &myService)

		// for each proxied port, find list of potential remote endpoints
		for _, endpoint := range service_imports {
			glog.V(2).Infof("Finding exports for import: %s %+v", endpoint.Application, endpoint)
			matchedEndpoint := false
			applicationRegex, err := regexp.Compile(fmt.Sprintf("^%s$", endpoint.Application))
			if err != nil {
				continue //Don't spam error message; it was reported at validation time
			}
			for _, ss := range states {
				hostPort, containerPort, protocol, match := ss.GetHostEndpointInfo(applicationRegex)
				if match {
					glog.V(1).Infof("Matched endpoint: %s.%s -> %s:%d (%s/%d)",
						myService.Name, endpoint.Application, ss.HostIP, hostPort, protocol, containerPort)
					// if port/protocol undefined in the import, use the export's values
					if endpoint.PortNumber != 0 {
						containerPort = endpoint.PortNumber
					}
					if endpoint.Protocol != "" {
						protocol = endpoint.Protocol
					}
					var ep dao.ApplicationEndpoint
					ep.Application = endpoint.Application
					ep.ServiceID = ss.ServiceID
					ep.ContainerPort = containerPort
					ep.HostPort = hostPort
					ep.HostIP = ss.HostIP
					ep.ContainerIP = ss.PrivateIP
					ep.Protocol = protocol
					ep.VirtualAddress = endpoint.VirtualAddress
					ep.InstanceID = ss.InstanceID

					key := fmt.Sprintf("%s:%d", protocol, containerPort)
					if _, exists := result[key]; !exists {
						result[key] = make([]*dao.ApplicationEndpoint, 0)
					}
					result[key] = append(result[key], &ep)
					matchedEndpoint = true
				}
			}
			if !matchedEndpoint {
				glog.V(1).Infof("Unmatched endpoint %s.%s", myService.Name, endpoint.Application)
			}
		}

		glog.V(2).Infof("Return for %s is %+v", serviceId, result)
	}
	return result, nil
}

// foundchild is an error used exclusively to short-circuit the service walking
// when an appropriate child has been found
type foundchild bool

// Satisfy the error interface
func (f foundchild) Error() string {
	return ""
}

// FindChildService walks services below the service specified by serviceId, checking to see
// if childName matches the service's name. If so, it returns it.
func (f *Facade) FindChildService(ctx datastore.Context, serviceId string, childName string) (*service.Service, error) {
	var child *service.Service

	visitor := func(svc *service.Service) error {
		if svc.Name == childName {
			child = svc
			// Short-circuit the rest of the walk
			return foundchild(true)
		}
		return nil
	}
	if err := f.walkServices(ctx, serviceId, visitor); err != nil {
		// If err is a foundchild we're just short-circuiting; otherwise it's a real err, pass it on
		if _, ok := err.(foundchild); !ok {
			return nil, err
		}
	}
	return child, nil
}

// start the provided service
func (f *Facade) StartService(ctx datastore.Context, serviceId string) error {
	glog.V(4).Infof("Facade.StartService %s", serviceId)
	// f will traverse all the services
	err := f.validateService(ctx, serviceId)
	glog.V(4).Infof("Facade.StartService validate service result %v", err)
	if err != nil {
		return err
	}

	visitor := func(svc *service.Service) error {
		//start f service
		svc.DesiredState = service.SVCRun
		err = f.updateService(ctx, svc)
		glog.V(4).Infof("Facade.StartService update service %v, %v: %v", svc.Name, svc.ID, err)
		if err != nil {
			return err
		}
		return nil
	}

	// traverse all the services
	return f.walkServices(ctx, serviceId, visitor)
}

// pause the provided service
func (f *Facade) PauseService(ctx datastore.Context, serviceID string) error {
	glog.V(4).Infof("Facade.PauseService %s", serviceID)

	visitor := func(svc *service.Service) error {
		svc.DesiredState = service.SVCPause
		if err := f.updateService(ctx, svc); err != nil {
			glog.Errorf("could not update service %+v due to error %s", svc, err)
			return err
		}
		glog.V(4).Infof("Facade.PauseService update service %v, %v", svc.Name, svc.ID)
		return nil
	}

	// traverse all the services
	return f.walkServices(ctx, serviceID, visitor)
}

func (f *Facade) StopService(ctx datastore.Context, id string) error {
	glog.V(0).Info("Facade.StopService id=", id)

	visitor := func(svc *service.Service) error {
		//start f service
		if svc.Launch == commons.MANUAL {
			return nil
		}
		svc.DesiredState = service.SVCStop
		if err := f.updateService(ctx, svc); err != nil {
			return err
		}
		return nil
	}

	// traverse all the services
	return f.walkServices(ctx, id, visitor)
}

type assignIPInfo struct {
	IP     string
	IPType string
	HostID string
}

func (f *Facade) retrievePoolIPs(ctx datastore.Context, poolID string) ([]assignIPInfo, error) {
	assignIPInfoSlice := []assignIPInfo{}

	poolIPs, err := f.GetPoolIPs(ctx, poolID)
	if err != nil {
		glog.Errorf("GetPoolIPs failed: %v", err)
		return assignIPInfoSlice, err
	}

	for _, hostIPResource := range poolIPs.HostIPs {
		anAssignIPInfo := assignIPInfo{IP: hostIPResource.IPAddress, IPType: "static", HostID: hostIPResource.HostID}
		assignIPInfoSlice = append(assignIPInfoSlice, anAssignIPInfo)
	}

	for _, virtualIP := range poolIPs.VirtualIPs {
		anAssignIPInfo := assignIPInfo{IP: virtualIP.IP, IPType: "virtual", HostID: ""}
		assignIPInfoSlice = append(assignIPInfoSlice, anAssignIPInfo)
	}

	return assignIPInfoSlice, nil
}

// assign an IP address to a service (and all its child services) containing non default AddressResourceConfig
func (f *Facade) AssignIPs(ctx datastore.Context, assignmentRequest dao.AssignmentRequest) error {
	myService, err := f.GetService(ctx, assignmentRequest.ServiceID)
	if err != nil {
		return err
	}

	assignIPInfoSlice, err := f.retrievePoolIPs(ctx, myService.PoolID)
	if err != nil {
		return err
	} else if len(assignIPInfoSlice) < 1 {
		return fmt.Errorf("no IPs available")
	}

	rand.Seed(time.Now().UTC().UnixNano())
	assignmentHostID := ""
	assignmentType := ""

	if assignmentRequest.AutoAssignment {
		// automatic IP requested
		glog.Infof("Automatic IP Address Assignment")
		randomIPIndex := rand.Intn(len(assignIPInfoSlice))

		assignmentRequest.IPAddress = assignIPInfoSlice[randomIPIndex].IP
		assignmentType = assignIPInfoSlice[randomIPIndex].IPType
		assignmentHostID = assignIPInfoSlice[randomIPIndex].HostID

		if assignmentType == "" {
			return fmt.Errorf("Assignment type could not be determined (virtual IP was likely not in the pool)")
		}
	} else {
		// manual IP provided
		// verify that the user provided IP address is available in the pool
		glog.Infof("Manual IP Address Assignment")

		for _, anAssignIPInfo := range assignIPInfoSlice {
			if assignmentRequest.IPAddress == anAssignIPInfo.IP {
				assignmentType = anAssignIPInfo.IPType
				assignmentHostID = anAssignIPInfo.HostID
			}
		}
		if assignmentType == "" {
			// IP was NOT contained in the pool
			return fmt.Errorf("requested IP address: %s is not contained in pool %s.", assignmentRequest.IPAddress, myService.PoolID)
		}
	}

	glog.Infof("Attempting to set IP address(es) to %s", assignmentRequest.IPAddress)

	assignments := []*addressassignment.AddressAssignment{}
	if err := f.GetServiceAddressAssignments(ctx, assignmentRequest.ServiceID, &assignments); err != nil {
		glog.Errorf("controlPlaneDao.GetServiceAddressAssignments failed in anonymous function: %v", err)
		return err
	}

	visitor := func(myService *service.Service) error {
		// if f service is in need of an IP address, assign it an IP address
		for _, endpoint := range myService.Endpoints {
			needsAnAddressAssignment, addressAssignmentId, err := f.needsAddressAssignment(ctx, myService.ID, endpoint)
			if err != nil {
				return err
			}

			// if an address assignment is needed (does not yet exist) OR
			// if a specific IP address is provided by the user AND an address assignment already exists
			if needsAnAddressAssignment || addressAssignmentId != "" {
				if addressAssignmentId != "" {
					glog.Infof("Removing AddressAssignment: %s", addressAssignmentId)
					err = f.RemoveAddressAssignment(ctx, addressAssignmentId)
					if err != nil {
						glog.Errorf("controlPlaneDao.RemoveAddressAssignment failed in AssignIPs anonymous function: %v", err)
						return err
					}
				}
				assignment := addressassignment.AddressAssignment{}
				assignment.AssignmentType = assignmentType
				assignment.HostID = assignmentHostID
				assignment.PoolID = myService.PoolID
				assignment.IPAddr = assignmentRequest.IPAddress
				assignment.Port = endpoint.AddressConfig.Port
				assignment.ServiceID = myService.ID
				assignment.EndpointName = endpoint.Name
				glog.Infof("Creating AddressAssignment for Endpoint: %s", assignment.EndpointName)

				var unusedStr string
				if err := f.AssignAddress(ctx, assignment, &unusedStr); err != nil {
					glog.Errorf("AssignAddress failed in AssignIPs anonymous function: %v", err)
					return err
				}

				if err := f.updateService(ctx, myService); err != nil {
					glog.Errorf("Failed to update service w/AssignAddressAssignment: %v", err)
					return err
				}

				glog.Infof("Created AddressAssignment: %s for Endpoint: %s", assignment.ID, assignment.EndpointName)
			}
		}
		return nil
	}

	// traverse all the services
	err = f.walkServices(ctx, assignmentRequest.ServiceID, visitor)
	if err != nil {
		return err
	}

	glog.Infof("All services requiring an explicit IP address (at f moment) from service: %v and down ... have been assigned: %s", assignmentRequest.ServiceID, assignmentRequest.IPAddress)
	return nil
}

//getService is an internal method that returns a Service without filling in all related service data like address assignments
//and modified config files
func (f *Facade) getService(ctx datastore.Context, id string) (service.Service, error) {
	glog.V(3).Infof("Facade.getService: id=%s", id)
	store := f.serviceStore
	svc, err := store.Get(datastore.Get(), id)
	if err != nil || svc == nil {
		return service.Service{}, err
	}
	return *svc, err
}

//getServices is an internal method that returns all Services without filling in all related service data like address assignments
//and modified config files
func (f *Facade) getServices(ctx datastore.Context) ([]*service.Service, error) {
	glog.V(3).Infof("Facade.GetServices")
	store := f.serviceStore
	results, err := store.GetServices(ctx)
	if err != nil {
		glog.Error("Facade.GetServices: err=", err)
		return results, err
	}
	return results, nil
}

//
func (f *Facade) getTenantIDAndPath(ctx datastore.Context, svc service.Service) (string, string, error) {
	gs := func(id string) (service.Service, error) {
		return f.getService(ctx, id)
	}

	tenantID, err := f.GetTenantID(ctx, svc.ID)
	if err != nil {
		return "", "", err
	}

	path, err := svc.GetPath(gs)
	if err != nil {
		return "", "", err
	}

	return tenantID, path, err
}

// traverse all the services (including the children of the provided service)
func (f *Facade) walkServices(ctx datastore.Context, serviceID string, visitFn service.Visit) error {
	store := f.serviceStore
	getChildren := func(parentID string) ([]*service.Service, error) {
		return store.GetChildServices(ctx, parentID)
	}
	getService := func(svcID string) (service.Service, error) {
		svc, err := store.Get(ctx, svcID)
		if err != nil {
			return service.Service{}, err
		}
		return *svc, nil
	}

	return service.Walk(serviceID, visitFn, getService, getChildren)
}

// walkTree returns a list of ids for all services in a hierarchy rooted by node
func walkTree(node *treenode) []string {
	if len(node.children) == 0 {
		return []string{node.id}
	}
	relatedServiceIDs := make([]string, 0)
	for _, childNode := range node.children {
		for _, childId := range walkTree(childNode) {
			relatedServiceIDs = append(relatedServiceIDs, childId)
		}
	}
	return append(relatedServiceIDs, node.id)
}

type treenode struct {
	id       string
	parent   string
	children []*treenode
}

// getServiceTree creates the service hierarchy tree containing serviceId, serviceList is used to create the tree.
// Returns a pointer the root of the service hierarchy
func (f *Facade) getServiceTree(serviceId string, servicesList *[]*service.Service) *treenode {
	glog.V(2).Infof(" getServiceTree = %s", serviceId)
	servicesMap := make(map[string]*treenode)
	for _, svc := range *servicesList {
		servicesMap[svc.ID] = &treenode{
			svc.ID,
			svc.ParentServiceID,
			[]*treenode{},
		}
	}

	// second time through builds our tree
	root := treenode{"root", "", []*treenode{}}
	for _, svc := range *servicesList {
		node := servicesMap[svc.ID]
		parent, found := servicesMap[svc.ParentServiceID]
		// no parent means f node belongs to root
		if !found {
			parent = &root
		}
		parent.children = append(parent.children, node)
	}

	// now walk up the tree, then back down capturing all siblings for f service ID
	topService := servicesMap[serviceId]
	for len(topService.parent) != 0 {
		topService = servicesMap[topService.parent]
	}
	return topService
}

// determine whether the services are ready for deployment
func (f *Facade) validateServicesForStarting(ctx datastore.Context, svc *service.Service) error {
	// ensure all endpoints with AddressConfig have assigned IPs
	for _, endpoint := range svc.Endpoints {
		needsAnAddressAssignment, addressAssignmentId, err := f.needsAddressAssignment(ctx, svc.ID, endpoint)
		if err != nil {
			return err
		}

		if needsAnAddressAssignment {
			return fmt.Errorf("service ID %s is in need of an AddressAssignment: %s", svc.ID, addressAssignmentId)
		} else if addressAssignmentId != "" {
			glog.Infof("AddressAssignment: %s already exists", addressAssignmentId)
		}

		if len(endpoint.VHosts) > 0 {
			//check to see if this vhost is in use by another app
		}
	}

	if svc.RAMCommitment < 0 {
		return fmt.Errorf("service RAM commitment cannot be negative")
	}

	// add additional validation checks to the services
	return nil
}

// validate the provided service
func (f *Facade) validateService(ctx datastore.Context, serviceId string) error {
	//TODO: create map of IPs to ports and ensure that an IP does not have > 1 process listening on the same port
	visitor := func(svc *service.Service) error {
		// validate the service is ready to start
		err := f.validateServicesForStarting(ctx, svc)
		if err != nil {
			glog.Errorf("services failed validation for starting")
			return err
		}
		for _, ep := range svc.GetServiceVHosts() {
			for _, vh := range ep.VHosts {
				//check that vhosts aren't already started elsewhere
				if err := zkAPI(f).CheckRunningVHost(vh, svc.ID); err != nil {
					return err
				}
			}
		}
		return nil
	}

	// traverse all the services
	if err := f.walkServices(ctx, serviceId, visitor); err != nil {
		glog.Errorf("unable to walk services for service %s", serviceId)
		return err
	}

	return nil
}

func (f *Facade) fillOutService(ctx datastore.Context, svc *service.Service) error {
	if err := f.fillServiceAddr(ctx, svc); err != nil {
		return err
	}
	if err := f.fillServiceConfigs(ctx, svc); err != nil {
		return err
	}
	return nil
}

func (f *Facade) fillOutServices(ctx datastore.Context, svcs []*service.Service) error {
	for _, svc := range svcs {
		if err := f.fillOutService(ctx, svc); err != nil {
			return err
		}
	}
	return nil
}

func (f *Facade) fillServiceConfigs(ctx datastore.Context, svc *service.Service) error {
	glog.V(3).Infof("fillServiceConfigs for %s", svc.ID)
	tenantID, servicePath, err := f.getTenantIDAndPath(ctx, *svc)
	if err != nil {
		return err
	}
	glog.V(3).Infof("service %v; tenantid=%s; path=%s", svc.ID, tenantID, servicePath)

	configStore := serviceconfigfile.NewStore()
	existingConfs, err := configStore.GetConfigFiles(ctx, tenantID, servicePath)
	if err != nil {
		return err
	}

	glog.Infof("Getting config for service %s: %v", svc.Name, svc.ConfigFiles)

	//found confs are the modified confs for f service
	foundConfs := make(map[string]*servicedefinition.ConfigFile)
	for _, svcConfig := range existingConfs {
		if confFile, ok := foundConfs[svcConfig.ConfFile.Filename]; !ok || confFile.Updated.Before(svcConfig.ConfFile.Updated) {
			foundConfs[svcConfig.ConfFile.Filename] = &svcConfig.ConfFile
		}
	}

	//replace with stored service config only if it is an existing config
	for name, conf := range foundConfs {
		if !conf.Deleted {
			svc.ConfigFiles[name] = *conf
		}
	}
	return nil
}

func (f *Facade) fillServiceAddr(ctx datastore.Context, svc *service.Service) error {
	addrs, err := f.getAddressAssignments(ctx, svc.ID)
	if err != nil {
		return err
	}
	for idx := range svc.Endpoints {
		if assignment, found := addrs[svc.Endpoints[idx].Name]; found {
			//assignment exists
			glog.V(4).Infof("setting address assignment on endpoint: %s, %v", svc.Endpoints[idx].Name, assignment)
			svc.Endpoints[idx].SetAssignment(assignment)
		} else {
			svc.Endpoints[idx].RemoveAssignment()
		}
	}
	return nil
}

// updateService internal method to use when service has been validated
func (f *Facade) updateService(ctx datastore.Context, svc *service.Service) error {
	id := strings.TrimSpace(svc.ID)
	if id == "" {
		return errors.New("empty Service.ID not allowed")
	}
	svc.ID = id
	//add assignment info to service so it is availble in zk
	f.fillServiceAddr(ctx, svc)

	svcStore := f.serviceStore

	oldSvc, err := svcStore.Get(ctx, svc.ID)
	if err != nil {
		return err
	}

	//Deal with Service Config Files

	// check if config files haven't changed
	if !reflect.DeepEqual(oldSvc.ConfigFiles, svc.ConfigFiles) {
		// lets validate the service before doing more work
		if err := svc.ValidEntity(); err != nil {
			return err
		}

		tenantID, servicePath, err := f.getTenantIDAndPath(ctx, *svc)
		if err != nil {
			return err
		}

		configStore := serviceconfigfile.NewStore()

		oldConfigs := oldSvc.ConfigFiles
		for key, conf := range svc.ConfigFiles {
			if oldConf, found := oldConfigs[key]; found {
				delete(oldConfigs, key)
				if reflect.DeepEqual(oldConf, conf) {
					continue
				}
			}
			conf.Updated = time.Now()
			newConf, err := serviceconfigfile.New(tenantID, servicePath, conf)
			if err != nil {
				return err
			}
			configStore.Put(ctx, serviceconfigfile.Key(newConf.ID), newConf)
		}
		for _, conf := range oldConfigs {
			conf.Content = ""
			conf.Deleted = true
			conf.Updated = time.Now()
			newConf, err := serviceconfigfile.New(tenantID, servicePath, conf)
			if err != nil {
				return nil
			}
			configStore.Put(ctx, serviceconfigfile.Key(newConf.ID), newConf)
		}
	}

	if err := svcStore.Put(ctx, svc); err != nil {
		return err
	}

	// Remove the service from zookeeper if the pool ID has changed
	err = nil
	if oldSvc.PoolID != svc.PoolID {
		err = zkAPI(f).removeService(oldSvc)
	}
	if err == nil {
		err = zkAPI(f).updateService(svc)
	}
	return err
}

type history []*servicedefinition.ConfigFile

func (h history) Len() int           { return len(h) }
func (h history) Less(x, y int) bool { return h[x].Updated.Before(h[y].Updated) }
func (h history) Swap(x, y int)      { h[x], h[y] = h[y], h[x] }

// Acquires the history of configuration changes for a service and arranges
// them in chronological order
func (f *Facade) ServiceConfigHistory(ctx datastore.Context, serviceID string) ([]*servicedefinition.ConfigFile, error) {
	svcStore := f.serviceStore
	svc, err := svcStore.Get(ctx, serviceID)
	if err != nil {
		return nil, err
	}

	tenantID, servicePath, err := f.getTenantIDAndPath(ctx, *svc)
	if err != nil {
		return nil, err
	}

	configStore := serviceconfigfile.NewStore()
	configs, err := configStore.GetConfigFiles(ctx, tenantID, servicePath)
	if err != nil {
		return nil, err
	}

	confHistory := make([]*servicedefinition.ConfigFile, len(configs))
	for i, config := range configs {
		confFile := config.ConfFile
		confHistory[i] = &confFile
	}

	// arrange in chronological order
	sort.Sort(history(confHistory))
	return confHistory, nil
}

func getZKAPI(f *Facade) zkfuncs {
	return &zkf{f}
}

type zkfuncs interface {
	updateService(svc *service.Service) error
	removeService(svc *service.Service) error
	getSvcStates(poolID string, serviceStates *[]*servicestate.ServiceState, serviceIds ...string) error
	RegisterHost(h *host.Host) error
	UnregisterHost(h *host.Host) error
	AddVirtualIP(vip *pool.VirtualIP) error
	RemoveVirtualIP(vip *pool.VirtualIP) error
	AddResourcePool(poolID string) error
	RemoveResourcePool(poolID string) error
	CheckRunningVHost(vhostName, serviceID string) error
}

type zkf struct {
	f *Facade
}

func (z *zkf) updateService(svc *service.Service) error {
	poolBasedConn, err := zzk.GetBasePathConnection(zzk.GeneratePoolPath(svc.PoolID))
	if err != nil {
		glog.Errorf("Error in getting a connection based on pool %v: %v", svc.PoolID, err)
		return err
	}
	return zkservice.UpdateService(poolBasedConn, svc)
}

func (z *zkf) removeService(svc *service.Service) error {
	poolBasedConn, err := zzk.GetBasePathConnection(zzk.GeneratePoolPath(svc.PoolID))
	if err != nil {
		glog.Errorf("Error in getting a connection based on pool %v: %v", svc.PoolID, err)
		return err
	}

	var (
		cancel = make(chan interface{})
		done   = make(chan interface{})
	)

	go func() {
		defer close(done)
		err = zkservice.RemoveService(cancel, poolBasedConn, svc.ID)
	}()

	go func() {
		defer close(cancel)
		<-time.After(30 * time.Second)
	}()

	<-done
	return err
}

func (z *zkf) getSvcStates(poolID string, serviceStates *[]*servicestate.ServiceState, serviceIDs ...string) error {
	poolBasedConn, err := zzk.GetBasePathConnection(zzk.GeneratePoolPath(poolID))
	if err != nil {
		glog.Errorf("Error in getting a connection based on pool %v: %v", poolID, err)
		return err
	}
	*serviceStates, err = zkservice.GetServiceStates(poolBasedConn, serviceIDs...)
	return err
}

func (z *zkf) RegisterHost(h *host.Host) error {
	poolBasedConnection, err := zzk.GetBasePathConnection(zzk.GeneratePoolPath(h.PoolID))
	if err != nil {
		return err
	}

	return zkservice.RegisterHost(poolBasedConnection, h.ID)
}

func (z *zkf) UnregisterHost(h *host.Host) error {
	poolBasedConnection, err := zzk.GetBasePathConnection(zzk.GeneratePoolPath(h.PoolID))
	if err != nil {
		return err
	}
	return zkservice.UnregisterHost(poolBasedConnection, h.ID)
}

func (z *zkf) AddVirtualIP(vip *pool.VirtualIP) error {
	poolBasedConnection, err := zzk.GetBasePathConnection(zzk.GeneratePoolPath(vip.PoolID))
	if err != nil {
		return err
	}
	return zkvirtualip.AddVirtualIP(poolBasedConnection, vip)
}

func (z *zkf) RemoveVirtualIP(vip *pool.VirtualIP) error {
	poolBasedConnection, err := zzk.GetBasePathConnection(zzk.GeneratePoolPath(vip.PoolID))
	if err != nil {
		return err
	}
	return zkvirtualip.RemoveVirtualIP(poolBasedConnection, vip.IP)
}

func (z *zkf) AddResourcePool(poolID string) error {
	rootBasedConnection, err := zzk.GetBasePathConnection("/")
	if err != nil {
		return err
	}
	return zkscheduler.AddResourcePool(rootBasedConnection, poolID)
}

func (z *zkf) RemoveResourcePool(poolID string) error {
	rootBasedConnection, err := zzk.GetBasePathConnection("/")
	if err != nil {
		return err
	}
	return zkscheduler.RemoveResourcePool(rootBasedConnection, poolID)
}

func (z *zkf) CheckRunningVHost(vhostName, serviceID string) error {
	rootBasedConnection, err := zzk.GetBasePathConnection("/")
	if err != nil {
		return err
	}

	vr, err := zkregistry.VHostRegistry(rootBasedConnection)
	if err != nil {
		glog.Errorf("Error getting vhost registry: %v", err)
		return err
	}

	vhostEphemeralNodes, err := vr.GetVHostKeyChildren(rootBasedConnection, vhostName)
	if err != nil {
		glog.Errorf("GetVHostKeyChildren failed %v: %v", vhostName, err)
		return err
	}
	if len(vhostEphemeralNodes) == 0 {
		glog.Warningf("Currently, there are no ephemeral nodes for vhost: %v", vhostName)
		return nil
	} else if len(vhostEphemeralNodes) > 1 {
		return fmt.Errorf("There is more than one ephemeral node for vhost: %v", vhostName)
	}

	for _, vhostEphemeralNode := range vhostEphemeralNodes {
		if vhostEphemeralNode.ServiceID == serviceID {
			glog.Infof("validated: vhost %v is already running under THIS servicedID: %v", vhostName, serviceID)
			return nil
		}
		return fmt.Errorf("failed validation: vhost %v is already running under a different serviceID")
	}

	return nil
}

func lookUpTenant(svcID string) (string, bool) {
	tenanIDMutex.RLock()
	defer tenanIDMutex.RUnlock()
	tID, found := tenantIDs[svcID]
	return tID, found
}

func updateTenants(tenantID string, svcIDs ...string) {
	tenanIDMutex.Lock()
	defer tenanIDMutex.Unlock()
	for _, id := range svcIDs {
		tenantIDs[id] = tenantID
	}
}

// GetTenantID calls its GetService function to get the tenantID
func getTenantID(svcID string, gs service.GetService) (string, error) {
	if tID, found := lookUpTenant(svcID); found {
		return tID, nil
	}

	svc, err := gs(svcID)
	if err != nil {
		return "", err
	}
	visitedIDs := make([]string, 0)
	visitedIDs = append(visitedIDs, svc.ID)
	for svc.ParentServiceID != "" {
		if tID, found := lookUpTenant(svc.ParentServiceID); found {
			return tID, nil
		}
		svc, err = gs(svc.ParentServiceID)
		if err != nil {
			return "", err
		}
		visitedIDs = append(visitedIDs, svc.ID)
	}

	updateTenants(svc.ID, visitedIDs...)
	return svc.ID, nil
}

var (
	tenantIDs    = make(map[string]string)
	tenanIDMutex = sync.RWMutex{}
)
