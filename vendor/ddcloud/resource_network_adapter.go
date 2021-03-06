package ddcloud

import (
	"fmt"
	"log"

	"github.com/DimensionDataResearch/dd-cloud-compute-terraform/models"
	"github.com/DimensionDataResearch/dd-cloud-compute-terraform/retry"
	"github.com/DimensionDataResearch/go-dd-cloud-compute/compute"
	"github.com/hashicorp/terraform/helper/schema"
)

const (
	resourceKeyNetworkAdapterServerID    = "server"
	resourceKeyNetworkAdapterMACAddress  = "mac"
	resourceKeyNetworkAdapterKey         = "mac"
	resourceKeyNetworkAdapterVLANID      = "vlan"
	resourceKeyNetworkAdapterPrivateIPV4 = "ipv4"
	resourceKeyNetworkAdapterPrivateIPV6 = "ipv6"
	resourceKeyNetworkAdapterType        = "type"
)

func resourceNetworkAdapter() *schema.Resource {
	return &schema.Resource{
		Create: resourceNetworkAdapterCreate,
		Exists: resourceNetworkAdapterExists,
		Read:   resourceNetworkAdapterRead,
		Update: resourceNetworkAdapterUpdate,
		Delete: resourceNetworkAdapterDelete,

		Schema: map[string]*schema.Schema{
			resourceKeyNetworkAdapterServerID: &schema.Schema{
				Type:        schema.TypeString,
				Required:    true,
				Description: "ID of the server to which the additional nics needs to be updated",
			},

			resourceKeyNetworkAdapterVLANID: &schema.Schema{
				Type:        schema.TypeString,
				Computed:    true,
				Optional:    true,
				Description: "VLAN ID of the nic",
				ForceNew:    true,
			},
			resourceKeyNetworkAdapterPrivateIPV4: &schema.Schema{
				Type:        schema.TypeString,
				Optional:    true,
				Computed:    true,
				Description: "Private IPV4 address for the nic",
			},
			resourceKeyNetworkAdapterPrivateIPV6: &schema.Schema{
				Type:        schema.TypeString,
				Computed:    true,
				Description: "Private IPV6 Address for the nic",
			},
			resourceKeyNetworkAdapterType: &schema.Schema{
				Type:         schema.TypeString,
				Optional:     true,
				ForceNew:     true,
				Default:      nil,
				Description:  "The type of network adapter (E1000 or VMXNET3)",
				ValidateFunc: validateNetworkAdapterAdapterType,
			},
		},
	}

}

func resourceNetworkAdapterCreate(data *schema.ResourceData, provider interface{}) error {
	propertyHelper := propertyHelper(data)
	serverID := data.Get(resourceKeyNetworkAdapterServerID).(string)
	ipv4Address := data.Get(resourceKeyNetworkAdapterPrivateIPV4).(string)
	vlanID := data.Get(resourceKeyNetworkAdapterVLANID).(string)
	adapterType := propertyHelper.GetOptionalString(resourceKeyNetworkAdapterType, false)

	log.Printf("Configure additional nics for server '%s'...", serverID)

	providerState := provider.(*providerState)
	providerSettings := providerState.Settings()
	apiClient := providerState.Client()

	server, err := apiClient.GetServer(serverID)
	if err != nil {
		return err
	}
	if server == nil {
		return fmt.Errorf("Cannot find server with '%s'", serverID)
	}

	isStarted := server.Started
	if isStarted {
		err = serverShutdown(providerState, serverID)
		if err != nil {
			return err
		}
	}

	log.Printf("Add network adapter to server '%s'...", serverID)

	var networkAdapterID string
	operationDescription := fmt.Sprintf("Add network adapter to server '%s'", serverID)
	err = providerState.Retry().Action(operationDescription, providerSettings.RetryTimeout, func(context retry.Context) {
		asyncLock := providerState.AcquireAsyncOperationLock(operationDescription)
		defer asyncLock.Release()

		var addError error
		if adapterType != nil {
			networkAdapterID, addError = apiClient.AddNicWithTypeToServer(serverID, ipv4Address, vlanID, *adapterType)
		} else {
			networkAdapterID, addError = apiClient.AddNicToServer(serverID, ipv4Address, vlanID)
		}

		if compute.IsResourceBusyError(addError) {
			context.Retry()
		} else if addError != nil {
			context.Fail(addError)
		}
	})
	if err != nil {
		return err
	}
	data.SetId(networkAdapterID)

	log.Printf("Adding network adapter '%s' to server '%s'...",
		networkAdapterID,
		serverID,
	)

	_, err = apiClient.WaitForChange(
		compute.ResourceTypeServer,
		serverID,
		"Add network adapter",
		resourceUpdateTimeoutServer,
	)
	if err != nil {
		return err
	}

	log.Printf("created the nic with the id %s", networkAdapterID)
	if isStarted {
		err = serverStart(providerState, serverID)
		if err != nil {
			return err
		}
	}

	log.Printf("Refresh properties for network adapter '%s' in server '%s'", networkAdapterID, serverID)
	server, err = apiClient.GetServer(serverID)
	if err != nil {
		return err
	}
	if server == nil {
		return fmt.Errorf("Cannot find server '%s'", serverID)
	}

	serverNetworkAdapters := models.NewNetworkAdaptersFromVirtualMachineNetwork(server.Network)
	serverNetworkAdapter := serverNetworkAdapters.GetByID(networkAdapterID)
	if serverNetworkAdapter == nil {
		data.SetId("") // NetworkAdapter deleted

		return fmt.Errorf("Newly-created network adapter (Id = '%s') not found", networkAdapterID)
	}
	if err != nil {
		return err
	}

	data.Set(resourceKeyNetworkAdapterPrivateIPV4, serverNetworkAdapter.PrivateIPv4Address)
	data.Set(resourceKeyNetworkAdapterVLANID, serverNetworkAdapter.VLANID)
	data.Set(resourceKeyNetworkAdapterPrivateIPV6, serverNetworkAdapter.PrivateIPv6Address)
	data.Set(resourceKeyNetworkAdapterPrivateIPV4, serverNetworkAdapter.PrivateIPv4Address)

	return nil
}

func resourceNetworkAdapterExists(data *schema.ResourceData, provider interface{}) (bool, error) {

	nicExists := false

	serverID := data.Get(resourceKeyNetworkAdapterServerID).(string)

	apiClient := provider.(*providerState).Client()

	nicID := data.Id()

	log.Printf("Get the server with the ID %s", serverID)

	server, err := apiClient.GetServer(serverID)

	if server == nil {
		log.Printf("server with the id %s cannot be found", serverID)
	}

	if err != nil {
		return nicExists, err
	}
	serverNetworkAdapters := server.Network.AdditionalNetworkAdapters
	for _, nic := range serverNetworkAdapters {

		if *nic.ID == nicID {
			nicExists = true
			break
		}
	}
	return nicExists, nil
}

func resourceNetworkAdapterRead(data *schema.ResourceData, provider interface{}) error {

	id := data.Id()

	serverID := data.Get(resourceKeyNetworkAdapterServerID).(string)

	log.Printf("Get the server with the ID %s", serverID)

	providerState := provider.(*providerState)
	apiClient := providerState.Client()
	server, err := apiClient.GetServer(serverID)
	if err != nil {
		return err
	}

	if server == nil {
		log.Printf("server with the id %s cannot be found", serverID)
	}

	serverNetworkAdapters := server.Network.AdditionalNetworkAdapters

	var serverNetworkAdapter compute.VirtualMachineNetworkAdapter
	for _, nic := range serverNetworkAdapters {
		if *nic.ID == id {
			serverNetworkAdapter = nic
			break
		}
	}

	if serverNetworkAdapter.ID == nil {
		log.Printf("NetworkAdapter with the id %s doesn't exists", id)
		data.SetId("") // NetworkAdapter deleted
		return nil
	}

	if err != nil {
		return err
	}
	data.Set(resourceKeyNetworkAdapterPrivateIPV4, serverNetworkAdapter.PrivateIPv4Address)
	data.Set(resourceKeyNetworkAdapterVLANID, serverNetworkAdapter.VLANID)
	data.Set(resourceKeyNetworkAdapterPrivateIPV6, serverNetworkAdapter.PrivateIPv6Address)
	data.Set(resourceKeyNetworkAdapterPrivateIPV4, serverNetworkAdapter.PrivateIPv4Address)

	return nil
}

func resourceNetworkAdapterUpdate(data *schema.ResourceData, provider interface{}) error {
	propertyHelper := propertyHelper(data)
	nicID := data.Id()
	serverID := data.Get(resourceKeyNetworkAdapterServerID).(string)
	privateIPV4 := propertyHelper.GetOptionalString(resourceKeyNetworkAdapterPrivateIPV4, true)

	providerState := provider.(*providerState)

	if data.HasChange(resourceKeyNetworkAdapterPrivateIPV4) {
		log.Printf("changing the ip address of the nic with the id %s to %s", nicID, *privateIPV4)
		err := updateNetworkAdapterIPAddress(providerState, serverID, nicID, privateIPV4)
		if err != nil {
			return err
		}
		log.Printf("IP address of the nic with the id %s changed to %s", nicID, *privateIPV4)
	}

	return nil
}

func resourceNetworkAdapterDelete(data *schema.ResourceData, provider interface{}) error {
	networkAdapterID := data.Id()
	serverID := data.Get(resourceKeyNetworkAdapterServerID).(string)

	providerState := provider.(*providerState)
	providerSettings := providerState.Settings()
	apiClient := providerState.Client()

	log.Printf("Removing network adapter '%s' from server '%s'...", networkAdapterID, serverID)

	server, err := apiClient.GetServer(serverID)
	if err != nil {
		return err
	}
	if server == nil {
		return fmt.Errorf("Cannot find server '%s'", serverID)
	}

	isStarted := server.Started
	if isStarted {
		err = serverShutdown(providerState, serverID)
		if err != nil {
			return err
		}
	}

	operationDescription := fmt.Sprintf("Remove network adapter '%s' from server '%s'", networkAdapterID, serverID)
	err = providerState.Retry().Action(operationDescription, providerSettings.RetryTimeout, func(context retry.Context) {
		asyncLock := providerState.AcquireAsyncOperationLock(operationDescription)
		defer asyncLock.Release()

		removeError := apiClient.RemoveNicFromServer(networkAdapterID)
		if removeError == nil {
			if compute.IsResourceBusyError(removeError) {
				context.Retry()
			} else {
				context.Fail(removeError)
			}
		}
	})
	if err != nil {
		return err
	}

	log.Printf("Removing network adapter with ID %s from server '%s'...",
		networkAdapterID,
		serverID,
	)
	_, err = apiClient.WaitForChange(
		compute.ResourceTypeServer,
		serverID,
		"Remove nic",
		resourceUpdateTimeoutServer,
	)
	if err != nil {
		return err
	}

	data.SetId("") // Resource deleted.

	log.Printf("Removed network adapter with ID %s from server '%s'.",
		networkAdapterID,
		serverID,
	)

	if isStarted {
		err = serverStart(providerState, serverID)
		if err != nil {
			return err
		}
	}

	return nil
}

// Notify the CloudControl infrastructure that a network adapter's IP address has changed.
func updateNetworkAdapterIPAddress(providerState *providerState, serverID string, networkAdapterID string, primaryIPv4 *string) error {
	log.Printf("Update IP address for network adapter '%s'...", networkAdapterID)

	providerSettings := providerState.Settings()
	apiClient := providerState.Client()

	operationDescription := fmt.Sprintf("Update IP address for network adapter '%s'", networkAdapterID)
	err := providerState.Retry().Action(operationDescription, providerSettings.RetryTimeout, func(context retry.Context) {
		// CloudControl has issues if more than one asynchronous operation is initated at a time (returns UNEXPECTED_ERROR).
		asyncLock := providerState.AcquireAsyncOperationLock(operationDescription)
		defer asyncLock.Release()

		notifyError := apiClient.NotifyServerIPAddressChange(networkAdapterID, primaryIPv4, nil)
		if compute.IsResourceBusyError(notifyError) {
			context.Retry()
		} else if notifyError != nil {
			context.Fail(notifyError)
		}
	})
	if err != nil {
		return err
	}

	compositeNetworkAdapterID := fmt.Sprintf("%s/%s", serverID, networkAdapterID)
	_, err = apiClient.WaitForChange(compute.ResourceTypeNetworkAdapter, compositeNetworkAdapterID, "Update adapter IP address", resourceUpdateTimeoutServer)

	return err
}

func validateNetworkAdapterAdapterType(value interface{}, propertyName string) (messages []string, errors []error) {
	if value == nil {
		return
	}

	adapterType, ok := value.(string)
	if !ok {
		errors = append(errors,
			fmt.Errorf("Unexpected value type '%v'", value),
		)

		return
	}

	switch adapterType {
	case compute.NetworkAdapterTypeE1000:
	case compute.NetworkAdapterTypeVMXNET3:
		break
	default:
		errors = append(errors,
			fmt.Errorf("Invalid network adapter type '%s'", value),
		)
	}

	return
}
