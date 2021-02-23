package redisenterprise

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/services/redisenterprise/mgmt/2021-03-01/redisenterprise"
	"github.com/hashicorp/terraform-plugin-sdk/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/helper/validation"
	"github.com/terraform-providers/terraform-provider-azurerm/azurerm/helpers/azure"
	"github.com/terraform-providers/terraform-provider-azurerm/azurerm/helpers/tf"
	"github.com/terraform-providers/terraform-provider-azurerm/azurerm/internal/clients"
	"github.com/terraform-providers/terraform-provider-azurerm/azurerm/internal/location"
	"github.com/terraform-providers/terraform-provider-azurerm/azurerm/internal/services/redisenterprise/parse"
	"github.com/terraform-providers/terraform-provider-azurerm/azurerm/internal/services/redisenterprise/validate"
	"github.com/terraform-providers/terraform-provider-azurerm/azurerm/internal/tags"
	azSchema "github.com/terraform-providers/terraform-provider-azurerm/azurerm/internal/tf/schema"
	"github.com/terraform-providers/terraform-provider-azurerm/azurerm/internal/timeouts"
	"github.com/terraform-providers/terraform-provider-azurerm/azurerm/utils"
)

func resourceRedisEnterpriseCluster() *schema.Resource {
	return &schema.Resource{
		Create: resourceRedisEnterpriseClusterCreate,
		Read:   resourceRedisEnterpriseClusterRead,
		Delete: resourceRedisEnterpriseClusterDelete,
		Importer: azSchema.ValidateResourceIDPriorToImport(func(id string) error {
			_, err := parse.RedisEnterpriseClusterID(id)
			return err
		}),

		Timeouts: &schema.ResourceTimeout{
			Create: schema.DefaultTimeout(30 * time.Minute),
			Read:   schema.DefaultTimeout(5 * time.Minute),
			Delete: schema.DefaultTimeout(30 * time.Minute),
		},

		Schema: map[string]*schema.Schema{
			"name": {
				Type:         schema.TypeString,
				Required:     true,
				ForceNew:     true,
				ValidateFunc: validate.RedisEnterpriseName,
			},

			"resource_group_name": azure.SchemaResourceGroupName(),

			"location": {
				Type:         schema.TypeString,
				Required:     true,
				ForceNew:     true,
				ValidateFunc: validate.RedisEnterpriseClusterLocation,
				StateFunc:    azure.NormalizeLocation,
			},

			"sku_name": {
				Type:         schema.TypeString,
				Required:     true,
				ForceNew:     true,
				ValidateFunc: validate.RedisEnterpriseClusterSkuName,
			},

			"zones": {
				Type:     schema.TypeList,
				Optional: true,
				ForceNew: true,
				MinItems: 1,
				Elem: &schema.Schema{
					Type: schema.TypeString,
					ValidateFunc: validation.StringInSlice([]string{
						"1",
						"2",
						"3",
					}, true),
				},
			},

			// RP currently does not return this value, but will in the near future (RP defaults to 1.2)
			// "minimum_tls_version": {
			// 	Type:     schema.TypeString,
			// 	Optional: true,
			// 	Default:  string(redisenterprise.OneFullStopTwo),
			// 	ValidateFunc: validation.StringInSlice([]string{
			// 		string(redisenterprise.OneFullStopZero),
			// 		string(redisenterprise.OneFullStopOne),
			// 		string(redisenterprise.OneFullStopTwo),
			// 	}, false),
			// },

			// RP currently does not return this value, but will in the near future
			"hostname": {
				Type:     schema.TypeString,
				Computed: true,
			},

			// RP currently does not return this value, but will in the near future
			"version": {
				Type:     schema.TypeString,
				Computed: true,
			},

			"tags": tags.ForceNewSchema(),
		},
	}
}

func resourceRedisEnterpriseClusterCreate(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*clients.Client).RedisEnterprise.Client
	ctx, cancel := timeouts.ForCreate(meta.(*clients.Client).StopContext, d)
	defer cancel()
	log.Printf("[INFO] preparing arguments for Redis Enterprise Cluster creation.")

	subscriptionId := meta.(*clients.Client).Account.SubscriptionId
	resourceId := parse.NewRedisEnterpriseClusterID(subscriptionId, d.Get("resource_group_name").(string), d.Get("name").(string))
	if d.IsNewResource() {
		existing, err := client.Get(ctx, resourceId.ResourceGroup, resourceId.Name)
		if err != nil {
			if !utils.ResponseWasNotFound(existing.Response) {
				return fmt.Errorf("checking for presence of existing Redis Enterprise Cluster (Name %q / Resource Group %q): %+v", resourceId.Name, resourceId.ResourceGroup, err)
			}
		}

		if !utils.ResponseWasNotFound(existing.Response) {
			return tf.ImportAsExistsError("azurerm_redis_enterprise_cluster", resourceId.ID())
		}
	}

	location := location.Normalize(d.Get("location").(string))
	sku := expandRedisEnterpriseClusterSku(d.Get("sku_name").(string))

	// If the sku type is flash check to make sure that the sku is supported in that region
	if strings.Contains(string(sku.Name), "Flash") {
		if err := validate.RedisEnterpriseClusterFlashSkuTypeLocation(location); err != nil {
			return fmt.Errorf("%s", err)
		}
	}

	parameters := redisenterprise.Cluster{
		Name:     utils.String(d.Get("name").(string)),
		Location: utils.String(location),
		Sku:      sku,
		Tags:     tags.Expand(d.Get("tags").(map[string]interface{})),
	}

	if v, ok := d.GetOk("zones"); ok {
		// Zones are currently not supported in these regions
		if location == "centraluseuap" || location == "westus" {
			return fmt.Errorf("Redis Enterprise Cluster (Name %q / Resource Group %q): 'Zones' are not currently supported in the 'West US' or 'Central US EUAP' regions, got %q", resourceId.Name, resourceId.ResourceGroup, location)
		}

		parameters.Zones = azure.ExpandZones(v.([]interface{}))
	}

	// RP currently does not return this value but will in the near future
	// if v, ok := d.GetOk("minimum_tls_version"); ok {
	// 	parameters.ClusterProperties.MinimumTLSVersion = redisenterprise.TLSVersion(v.(string))
	// }

	future, err := client.Create(ctx, resourceId.ResourceGroup, resourceId.Name, parameters)
	if err != nil {
		return fmt.Errorf("waiting for creation of Redis Enterprise Cluster (Name %q / Resource Group %q): %+v", resourceId.Name, resourceId.ResourceGroup, err)
	}

	if err = future.WaitForCompletionRef(ctx, client.Client); err != nil {
		return fmt.Errorf("waiting for the creation of Redis Enterprise Cluster (Name %q / Resource Group %q): %+v", resourceId.Name, resourceId.ResourceGroup, err)
	}

	log.Printf("[DEBUG] Waiting for Redis Enterprise Cluster (Name %q / Resource Group %q) to become available", resourceId.Name, resourceId.ResourceGroup)
	stateConf := &resource.StateChangeConf{
		Pending:    []string{"Creating", "Updating", "Enabling", "Deleting", "Disabling"},
		Target:     []string{"Running"},
		Refresh:    redisEnterpriseClusterStateRefreshFunc(ctx, client, resourceId),
		MinTimeout: 15 * time.Second,
		Timeout:    d.Timeout(schema.TimeoutCreate),
	}

	if _, err = stateConf.WaitForState(); err != nil {
		return fmt.Errorf("waiting for Redis Enterprise Cluster (Name %q / Resource Group %q) to become available: %+v", resourceId.Name, resourceId.ResourceGroup, err)
	}

	d.SetId(resourceId.ID())

	return resourceRedisEnterpriseClusterRead(d, meta)
}

func resourceRedisEnterpriseClusterRead(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*clients.Client).RedisEnterprise.Client
	ctx, cancel := timeouts.ForRead(meta.(*clients.Client).StopContext, d)
	defer cancel()

	id, err := parse.RedisEnterpriseClusterID(d.Id())
	if err != nil {
		return err
	}

	resp, err := client.Get(ctx, id.ResourceGroup, id.Name)
	if err != nil {
		if utils.ResponseWasNotFound(resp.Response) {
			log.Printf("[DEBUG] Redis Enterprise Cluster (Name %q / Resource Group %q) was not found - removing from state!", id.Name, id.ResourceGroup)
			d.SetId("")
			return nil
		}

		return fmt.Errorf("retrieving Redis Enterprise Cluster (Name %q / Resource Group %q): %+v", id.Name, id.ResourceGroup, err)
	}

	d.Set("name", id.Name)
	d.Set("resource_group_name", id.ResourceGroup)
	d.Set("location", location.NormalizeNilable(resp.Location))

	if err := d.Set("sku_name", flattenRedisEnterpriseClusterSku(resp.Sku)); err != nil {
		return fmt.Errorf("setting `sku_name`: %+v", err)
	}

	if zones := resp.Zones; zones != nil {
		d.Set("zones", zones)
	}

	if props := resp.ClusterProperties; props != nil {
		d.Set("hostname", props.HostName)
		d.Set("version", props.RedisVersion)
		// RP currently does not return this value
		// d.Set("minimum_tls_version", string(props.MinimumTLSVersion))
	}

	return tags.FlattenAndSet(d, resp.Tags)
}

func resourceRedisEnterpriseClusterDelete(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*clients.Client).RedisEnterprise.Client
	ctx, cancel := timeouts.ForDelete(meta.(*clients.Client).StopContext, d)
	defer cancel()

	id, err := parse.RedisEnterpriseClusterID(d.Id())
	if err != nil {
		return err
	}

	future, err := client.Delete(ctx, id.ResourceGroup, id.Name)
	if err != nil {
		return fmt.Errorf("deleting Redis Enterprise Cluster (Name %q / Resource Group %q): %+v", id.Name, id.ResourceGroup, err)
	}

	if err = future.WaitForCompletionRef(ctx, client.Client); err != nil {
		return fmt.Errorf("waiting for the deletion of Redis Enterprise Cluster (Name %q / Resource Group %q): %+v", id.Name, id.ResourceGroup, err)
	}

	return nil
}

func expandRedisEnterpriseClusterSku(v string) *redisenterprise.Sku {
	redisSku, _ := parse.RedisEnterpriseCacheSkuName(v)
	capacity, _ := strconv.ParseInt(redisSku.Capacity, 10, 32)

	sku := &redisenterprise.Sku{
		Name:     redisenterprise.SkuName(redisSku.Name),
		Capacity: utils.Int32(int32(capacity)),
	}

	return sku
}

func flattenRedisEnterpriseClusterSku(input *redisenterprise.Sku) *string {
	if input == nil {
		return nil
	}

	var name redisenterprise.SkuName
	var capacity int32

	if input.Name != "" {
		name = input.Name
	}

	capacity = *input.Capacity

	skuName := fmt.Sprintf("%s-%d", name, capacity)

	return &skuName
}

func redisEnterpriseClusterStateRefreshFunc(ctx context.Context, client *redisenterprise.Client, id parse.RedisEnterpriseClusterId) resource.StateRefreshFunc {
	return func() (interface{}, string, error) {
		res, err := client.Get(ctx, id.ResourceGroup, id.Name)
		if err != nil {
			return nil, "", fmt.Errorf("retrieving status of Redis Enterprise Cluster (Name %q / Resource Group %q): %+v", id.Name, id.ResourceGroup, err)
		}

		return res, string(res.ClusterProperties.ResourceState), nil
	}
}
