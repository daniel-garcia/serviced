/* global controlplane: true */

/* PoolDetailsController
 * Displays details of a specific pool
 */
(function() {
    'use strict';

    controlplane.controller("PoolDetailsController", ["$scope", "$routeParams", "$location", "resourcesFactory", "authService", "$modalService", "$translate", "$notification", "miscUtils", "hostsFactory", "poolsFactory",
    function($scope, $routeParams, $location, resourcesFactory, authService, $modalService, $translate, $notification, utils, hostsFactory, poolsFactory){
        // Ensure logged in
        authService.checkLogin($scope);

        $scope.name = "pooldetails";
        $scope.params = $routeParams;

        $scope.add_virtual_ip = {};

        $scope.breadcrumbs = [
            { label: 'breadcrumb_pools', url: '#/pools' }
        ];

        // Build metadata for displaying a pool's virtual ips
        $scope.virtual_ip_addresses = utils.buildTable('IP', [
            { id: 'IP', name: 'pool_tbl_virtual_ip_address_ip'},
            { id: 'Netmask', name: 'pool_tbl_virtual_ip_address_netmask'},
            { id: 'BindInterface', name: 'pool_tbl_virtual_ip_address_bind_interface'},
            { id: 'Actions', name: 'pool_tbl_virtual_ip_address_action'}
        ]);

        //
        // Scope methods
        //

        // Pool view action - delete
        $scope.clickRemoveVirtualIp = function(ip) {
            $modalService.create({
                template: $translate.instant("confirm_remove_virtual_ip") + " <strong>"+ ip.IP +"</strong>",
                model: $scope,
                title: "remove_virtual_ip",
                actions: [
                    {
                        role: "cancel"
                    },{
                        role: "ok",
                        label: "remove_virtual_ip",
                        classes: "btn-danger",
                        action: function(){
                            resourcesFactory.remove_pool_virtual_ip(ip.PoolID, ip.IP, function() {
                                poolsFactory.update();
                            });
                            this.close();
                        }
                    }
                ]
            });
        };

        // Add Virtual Ip Modal - Add button action
        $scope.addVirtualIp = function(pool) {
            var ip = $scope.add_virtual_ip;

            return resourcesFactory.add_pool_virtual_ip(ip.PoolID, ip.IP, ip.Netmask, ip.BindInterface)
                .success(function(data, status){
                    $scope.add_virtual_ip = {};
                    $notification.create("Added new pool virtual ip", ip).success();
                    poolsFactory.update();
                });
        };

        // Open the virtual ip modal
        $scope.modalAddVirtualIp = function(pool) {
            $scope.add_virtual_ip = {'PoolID': pool.id, 'IP':"", 'Netmask':"", 'BindInterface':""};
            $modalService.create({
                templateUrl: "pool-add-virtualip.html",
                model: $scope,
                title: "add_virtual_ip",
                actions: [
                    {
                        role: "cancel",
                        action: function(){
                            $scope.add_virtual_ip = {};
                            this.close();
                        }
                    },{
                        role: "ok",
                        label: "add_virtual_ip",
                        action: function(){
                            if(this.validate()){
                                // disable ok button, and store the re-enable function
                                var enableSubmit = this.disableSubmitButton();

                                $scope.addVirtualIp($scope.add_virtual_ip)
                                    .success(function(data, status){
                                        this.close();
                                    }.bind(this))
                                    .error(function(data, status){
                                       this.createNotification("Adding pool virtual ip failed", data.Detail).error();
                                       enableSubmit();
                                    }.bind(this));
                            }
                        }
                    }
                ]
            });
        };

        // route host clicks to host page
        $scope.clickHost = function(hostId) {
            $location.path('/hosts/' + hostId);
        };

        // Ensure we have a list of pools
        poolsFactory.update()
            .then(() => {
                $scope.currentPool = poolsFactory.get($scope.params.poolID);
                if ($scope.currentPool) {
                    $scope.breadcrumbs.push({label: $scope.currentPool.id, itemClass: 'active'});

                    hostsFactory.update()
                        .then(() => {
                           // reduce the list to hosts associated with this pool
                            $scope.hosts = hostsFactory.hostList.filter(function(host){
                                return host.model.PoolID === $scope.currentPool.id;
                            });
                        });
                }
            });
    }]);
})();
