package ddcloud

import (
	"fmt"

	"github.com/hashicorp/terraform/helper/resource"
	"github.com/hashicorp/terraform/terraform"
)

// Acceptance test configuration - ddcloud_server with single network_adapter (primary with IPv4 address)
func testAccDDCloudServerNetworkAdapterPrimaryWithIPV4Address(name string, description string, vlanBaseIPAddress string) string {
	return fmt.Sprintf(`
		provider "ddcloud" {
			region		= "AU"
			allow_server_reboot = true
		}
		variable "vlan_network" { default = "%s" }
		resource "ddcloud_networkdomain" "acc_test_domain" {
			name		= "acc-test-networkdomain"
			description	= "Network domain for Terraform acceptance test."
			datacenter	= "AU10"
		}
		resource "ddcloud_vlan" "acc_test_vlan" {
			name				= "acc-test-vlan"
			description 		= "VLAN for Terraform acceptance test."
			networkdomain 		= "${ddcloud_networkdomain.acc_test_domain.id}"
			ipv4_base_address	= "${element(split("/", var.vlan_network), 0)}"
			ipv4_prefix_size	= "${element(split("/", var.vlan_network), 1)}"
			depends_on = ["ddcloud_networkdomain.acc_test_domain"]
		}
		resource "ddcloud_server" "acc_test_server" {
			name				 = "%s"
			description 		 = "%s"
			admin_password		 = "snausages!"
			memory_gb			 = 8
			networkdomain 		 = "${ddcloud_networkdomain.acc_test_domain.id}"
			dns_primary			 = "8.8.8.8"
			dns_secondary		 = "8.8.4.4"
			os_image_name		 = "CentOS 7 64-bit 2 CPU"
			auto_start			 = false
			# Image disk
			disk {
				scsi_unit_id     = 0
				size_gb          = 10
				speed            = "STANDARD"
			}
			network_adapter {
				ipv4 = "${cidrhost(var.vlan_network, 20)}"
			}
			depends_on = ["ddcloud_vlan.acc_test_vlan"]
		}
	`, vlanBaseIPAddress, name, description)
}

// Check if the additional nic configuration matches the expected configuration.
func testCheckDDCloudServerNICMatchesIPV4(serverResourceName string, expected string) resource.TestCheckFunc {
	return func(state *terraform.State) error {

		serverResource, ok := state.RootModule().Resources[serverResourceName]
		if !ok {
			return fmt.Errorf("Not found: %s", serverResourceName)
		}

		serverID := serverResource.Primary.ID

		client := testAccProvider.Meta().(*providerState).Client()
		server, err := client.GetServer(serverID)
		if err != nil {
			return fmt.Errorf("Bad: Get server: %s", err)
		}
		if server == nil {
			return fmt.Errorf("Bad: Server not found with Id '%s'", serverID)
		}

		if len(server.Network.AdditionalNetworkAdapters) == 0 {
			return fmt.Errorf("Bad: Server '%s' has no additional nics", serverID)
		}

		isNicExists := false
		for _, networkAdapters := range server.Network.AdditionalNetworkAdapters {
			if *networkAdapters.PrivateIPv4Address == expected {
				isNicExists = true
			}
		}
		if !isNicExists {
			return fmt.Errorf("Bad: Server '%s' doesn't have additional nic with the ip address (expected %s) ", serverID, expected)
		}
		return nil
	}
}

// Check if the additional nic configuration matches the expected configuration.
func testCheckDDCloudServerNICMatchesVLANID(serverResourceName string, vlanResourceName string) resource.TestCheckFunc {
	return func(state *terraform.State) error {

		serverResource, ok := state.RootModule().Resources[serverResourceName]
		if !ok {
			return fmt.Errorf("Not found: %s", serverResourceName)
		}

		serverID := serverResource.Primary.ID

		vlanResource, ok := state.RootModule().Resources[vlanResourceName]
		if !ok {
			return fmt.Errorf("Not found: %s", vlanResourceName)
		}

		vlanID := vlanResource.Primary.ID

		client := testAccProvider.Meta().(*providerState).Client()
		server, err := client.GetServer(serverID)
		if err != nil {
			return fmt.Errorf("Bad: Get server: %s", err)
		}
		if server == nil {
			return fmt.Errorf("Bad: Server not found with Id '%s'", serverID)
		}

		if len(server.Network.AdditionalNetworkAdapters) == 0 {
			return fmt.Errorf("Bad: Server '%s' has no additional nics", serverID)
		}
		isNicExists := false
		for _, networkAdapters := range server.Network.AdditionalNetworkAdapters {
			if *networkAdapters.VLANID == vlanID {
				isNicExists = true
			}
		}
		if !isNicExists {
			return fmt.Errorf("Bad: Server '%s' doesn't have additional nic with the vlanID (expected %s) ", serverID, vlanID)
		}
		return nil
	}
}

// Check all ServerNICs specified in the configuration have been destroyed.
func testCheckDDCloudServerNICDestroy(state *terraform.State) error {
	for _, res := range state.RootModule().Resources {
		if res.Type != "ddcloud_server" {
			continue
		}

		serverID := res.Primary.ID

		client := testAccProvider.Meta().(*providerState).Client()
		server, err := client.GetServer(serverID)
		if err != nil {
			return nil
		}
		if server != nil {
			nics := server.Network.AdditionalNetworkAdapters
			for _, nic := range nics {
				return fmt.Errorf("Nic '%s' still exists", *nic.ID)
			}
		}

	}
	return nil
}
