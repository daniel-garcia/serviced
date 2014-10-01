// Copyright 2014 The Serviced Authors.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package agent implements a service that runs on a serviced node. It is
// responsible for ensuring that a particular node is running the correct services
// and reporting the state and health of those services back to the master
// serviced.

package node

import (
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"

	"github.com/control-center/serviced/dao"
	"github.com/control-center/serviced/domain"
	"github.com/control-center/serviced/domain/addressassignment"
	"github.com/control-center/serviced/domain/service"
	"github.com/control-center/serviced/domain/servicestate"
	"github.com/control-center/serviced/domain/servicetemplate"
	"github.com/control-center/serviced/domain/user"
	"github.com/zenoss/glog"
)

// A serviced client.
type ControlClient struct {
	addr      string
	rpcClient *rpc.Client
}

// Ensure that ControlClient implements the ControlPlane interface.
var _ dao.ControlPlane = &ControlClient{}

// Create a new ControlClient.
func NewControlClient(addr string) (s *ControlClient, err error) {
	s = new(ControlClient)
	s.addr = addr
	glog.V(4).Infof("Connecting to %s", addr)
	conn, err := net.Dial("tcp", s.addr)
	if err != nil {
		return nil, err
	}
	s.rpcClient = jsonrpc.NewClient(conn)
	return s, nil
}

// Return the matching hosts.
func (s *ControlClient) Close() (err error) {
	return s.rpcClient.Close()
}

func (s *ControlClient) GetServiceEndpoints(serviceId string, response *map[string][]dao.ApplicationEndpoint) (err error) {
	return s.rpcClient.Call("ControlPlane.GetServiceEndpoints", serviceId, response)
}

func (s *ControlClient) GetServices(request dao.ServiceRequest, replyServices *[]service.Service) (err error) {
	return s.rpcClient.Call("ControlPlane.GetServices", request, replyServices)
}

func (s *ControlClient) GetTaggedServices(request dao.ServiceRequest, replyServices *[]service.Service) (err error) {
	return s.rpcClient.Call("ControlPlane.GetTaggedServices", request, replyServices)
}

func (s *ControlClient) GetService(serviceId string, service *service.Service) (err error) {
	return s.rpcClient.Call("ControlPlane.GetService", serviceId, &service)
}

func (s *ControlClient) FindChildService(request dao.FindChildRequest, service *service.Service) (err error) {
	return s.rpcClient.Call("ControlPlane.FindChildService", request, &service)
}

func (s *ControlClient) GetTenantId(serviceId string, tenantId *string) (err error) {
	return s.rpcClient.Call("ControlPlane.GetTenantId", serviceId, tenantId)
}

func (s *ControlClient) AddService(service service.Service, serviceId *string) (err error) {
	return s.rpcClient.Call("ControlPlane.AddService", service, serviceId)
}

func (s *ControlClient) DeployService(service dao.ServiceDeploymentRequest, serviceId *string) (err error) {
	return s.rpcClient.Call("ControlPlane.DeployService", service, serviceId)
}

func (s *ControlClient) UpdateService(service service.Service, unused *int) (err error) {
	return s.rpcClient.Call("ControlPlane.UpdateService", service, unused)
}

func (s *ControlClient) RemoveService(serviceId string, unused *int) (err error) {
	return s.rpcClient.Call("ControlPlane.RemoveService", serviceId, unused)
}

func (s *ControlClient) AssignIPs(assignmentRequest dao.AssignmentRequest, _ *struct{}) (err error) {
	return s.rpcClient.Call("ControlPlane.AssignIPs", assignmentRequest, nil)
}

func (s *ControlClient) GetServiceAddressAssignments(serviceID string, addresses *[]addressassignment.AddressAssignment) (err error) {
	return s.rpcClient.Call("ControlPlane.GetServiceAddressAssignments", serviceID, addresses)
}

func (s *ControlClient) GetServiceLogs(serviceId string, logs *string) error {
	return s.rpcClient.Call("ControlPlane.GetServiceLogs", serviceId, logs)
}

func (s *ControlClient) GetServiceStateLogs(request dao.ServiceStateRequest, logs *string) error {
	return s.rpcClient.Call("ControlPlane.GetServiceStateLogs", request, logs)
}

func (s *ControlClient) GetRunningServicesForHost(hostId string, runningServices *[]dao.RunningService) (err error) {
	return s.rpcClient.Call("ControlPlane.GetRunningServicesForHost", hostId, runningServices)
}

func (s *ControlClient) GetRunningServicesForService(serviceId string, runningServices *[]dao.RunningService) (err error) {
	return s.rpcClient.Call("ControlPlane.GetRunningServicesForService", serviceId, runningServices)
}

func (s *ControlClient) StopRunningInstance(request dao.HostServiceRequest, unused *int) (err error) {
	return s.rpcClient.Call("ControlPlane.StopRunningInstance", request, unused)
}

func (s *ControlClient) GetRunningServices(request dao.EntityRequest, runningServices *[]dao.RunningService) (err error) {
	return s.rpcClient.Call("ControlPlane.GetRunningServices", request, runningServices)
}

func (s *ControlClient) GetServiceState(request dao.ServiceStateRequest, state *servicestate.ServiceState) error {
	return s.rpcClient.Call("ControlPlane.GetServiceState", request, state)
}

func (s *ControlClient) GetRunningService(request dao.ServiceStateRequest, running *dao.RunningService) error {
	return s.rpcClient.Call("ControlPlane.GetRunningService", request, running)
}

func (s *ControlClient) GetServiceStates(serviceId string, states *[]servicestate.ServiceState) (err error) {
	return s.rpcClient.Call("ControlPlane.GetServiceStates", serviceId, states)
}

func (s *ControlClient) StartService(serviceId string, hostId *string) (err error) {
	return s.rpcClient.Call("ControlPlane.StartService", serviceId, hostId)
}

func (s *ControlClient) RestartService(serviceId string, unused *int) (err error) {
	return s.rpcClient.Call("ControlPlane.RestartService", serviceId, unused)
}

func (s *ControlClient) StopService(serviceId string, unused *int) (err error) {
	return s.rpcClient.Call("ControlPlane.StopService", serviceId, unused)
}

func (s *ControlClient) UpdateServiceState(state servicestate.ServiceState, unused *int) (err error) {
	return s.rpcClient.Call("ControlPlane.UpdateServiceState", state, unused)
}

func (s *ControlClient) GetServiceStatus(serviceID string, statusmap *map[string]dao.ServiceStatus) (err error) {
	return s.rpcClient.Call("ControlPlane.GetServiceStatus", serviceID, statusmap)
}

func (s *ControlClient) DeployTemplate(request dao.ServiceTemplateDeploymentRequest, tenantId *string) error {
	return s.rpcClient.Call("ControlPlane.DeployTemplate", request, tenantId)
}

func (s *ControlClient) DeployTemplateStatus(request dao.ServiceTemplateDeploymentRequest, status *string) error {
	return s.rpcClient.Call("ControlPlane.DeployTemplateStatus", request, status)
}

func (s *ControlClient) DeployTemplateActive(notUsed string, active *[]map[string]string) error {
	return s.rpcClient.Call("ControlPlane.DeployTemplateActive", notUsed, active)
}

func (s *ControlClient) GetServiceTemplates(unused int, serviceTemplates *map[string]servicetemplate.ServiceTemplate) error {
	return s.rpcClient.Call("ControlPlane.GetServiceTemplates", unused, serviceTemplates)
}

func (s *ControlClient) AddServiceTemplate(serviceTemplate servicetemplate.ServiceTemplate, templateId *string) error {
	return s.rpcClient.Call("ControlPlane.AddServiceTemplate", serviceTemplate, templateId)
}

func (s *ControlClient) UpdateServiceTemplate(serviceTemplate servicetemplate.ServiceTemplate, unused *int) error {
	return s.rpcClient.Call("ControlPlane.UpdateServiceTemplate", serviceTemplate, unused)
}

func (s *ControlClient) RemoveServiceTemplate(serviceTemplateID string, unused *int) error {
	return s.rpcClient.Call("ControlPlane.RemoveServiceTemplate", serviceTemplateID, unused)
}

func (s *ControlClient) DeleteSnapshot(snapshotId string, unused *int) error {
	return s.rpcClient.Call("ControlPlane.DeleteSnapshot", snapshotId, unused)
}

func (s *ControlClient) DeleteSnapshots(serviceId string, unused *int) error {
	return s.rpcClient.Call("ControlPlane.DeleteSnapshots", serviceId, unused)
}

func (s *ControlClient) Rollback(serviceId string, unused *int) error {
	return s.rpcClient.Call("ControlPlane.Rollback", serviceId, unused)
}

func (s *ControlClient) Snapshot(serviceId string, label *string) error {
	return s.rpcClient.Call("ControlPlane.Snapshot", serviceId, label)
}

func (s *ControlClient) AsyncSnapshot(serviceId string, label *string) error {
	return s.rpcClient.Call("ControlPlane.AsyncSnapshot", serviceId, label)
}

func (s *ControlClient) ListSnapshots(serviceId string, labels *[]string) error {
	return s.rpcClient.Call("ControlPlane.ListSnapshots", serviceId, labels)
}

func (s *ControlClient) Commit(containerId string, label *string) error {
	return s.rpcClient.Call("ControlPlane.Commit", containerId, label)
}

func (s *ControlClient) ReadyDFS(unused bool, unusedint *int) error {
	return s.rpcClient.Call("ControlPlane.ReadyDFS", unused, unusedint)
}

func (s *ControlClient) Backup(backupDirectory string, backupFilePath *string) error {
	return s.rpcClient.Call("ControlPlane.Backup", backupDirectory, backupFilePath)
}

func (s *ControlClient) AsyncBackup(backupDirectory string, backupFilePath *string) error {
	return s.rpcClient.Call("ControlPlane.AsyncBackup", backupDirectory, backupFilePath)
}

func (s *ControlClient) Restore(backupFilePath string, unused *int) error {
	return s.rpcClient.Call("ControlPlane.Restore", backupFilePath, unused)
}

func (s *ControlClient) AsyncRestore(backupFilePath string, unused *int) error {
	return s.rpcClient.Call("ControlPlane.AsyncRestore", backupFilePath, unused)
}

func (s *ControlClient) BackupStatus(notUsed int, backupStatus *string) error {
	return s.rpcClient.Call("ControlPlane.BackupStatus", notUsed, backupStatus)
}

func (s *ControlClient) ImageLayerCount(imageUUID string, layers *int) error {
	return s.rpcClient.Call("ControlPlane.ImageLayerCount", imageUUID, layers)
}

func (s *ControlClient) ValidateCredentials(user user.User, result *bool) error {
	return s.rpcClient.Call("ControlPlane.ValidateCredentials", user, result)
}

func (s *ControlClient) GetSystemUser(unused int, user *user.User) error {
	return s.rpcClient.Call("ControlPlane.GetSystemUser", unused, user)
}

func (s *ControlClient) Action(req dao.AttachRequest, unused *int) error {
	return s.rpcClient.Call("ControlPlane.Action", req, unused)
}

func (s *ControlClient) LogHealthCheck(result domain.HealthCheckResult, unused *int) error {
	return s.rpcClient.Call("ControlPlane.LogHealthCheck", result, unused)
}
