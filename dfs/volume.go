// Copyright 2014 The Serviced Authors.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package dfs

import (
	"errors"
	"os"
	"os/user"
	"path"
	"path/filepath"
	"strings"

	"github.com/control-center/serviced/domain/service"
	"github.com/control-center/serviced/volume"
	"github.com/zenoss/glog"
)

func (dfs *DistributedFilesystem) getVolume(svc *service.Service) (*volume.Volume, error) {
	v, err := getSubvolume(dfs.vfs, svc.PoolID, svc.ID)
	if err != nil {
		glog.Errorf("Could not acquire subvolume for service %s (%s): %s", svc.Name, svc.ID, err)
		return nil, err
	} else if v == nil {
		err := errors.New("volume is nil")
		glog.Errorf("Could not get volume for service %s (%s): %s", svc.Name, svc.ID, err)
		return nil, err
	}

	return v, nil
}

func getSubvolume(vfs, poolID, serviceID string) (*volume.Volume, error) {
	baseDir, err := filepath.Abs(path.Join(getVarPath(), "volumes", poolID))
	if err != nil {
		return nil, err
	}
	glog.Infof("Mounting vfs: %v; tenantID: %v; baseDir: %v", vfs, serviceID, baseDir)
	return volume.Mount(vfs, serviceID, baseDir)
}

func getVarPath() string {
	if servicedHome := strings.TrimSpace(os.Getenv("SERVICED_HOME")); servicedHome != "" {
		return path.Join(servicedHome, "var")
	} else if user, err := user.Current(); err == nil {
		return path.Join(os.TempDir(), "serviced-"+user.Username, "var")
	} else {
		defaultPath := "/tmp/serviced/var"
		glog.Warningf("Defaulting varPath to %v", defaultPath)
		return defaultPath
	}
}
