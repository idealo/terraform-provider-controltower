package provider

import (
	"context"
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/organizations"
	"github.com/aws/aws-sdk-go/service/servicecatalog"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

var (
	invalidProductNameChars = regexp.MustCompile("[^a-zA-Z0-9._-]")
)

func resourceAWSAccount() *schema.Resource {
	return &schema.Resource{
		Description: "Provides an AWS account resource via Control Tower.",

		CreateContext: resourceAWSAccountCreate,
		ReadContext:   resourceAWSAccountRead,
		UpdateContext: resourceAWSAccountUpdate,
		DeleteContext: resourceAWSAccountDelete,
		Importer: &schema.ResourceImporter{
			StateContext: schema.ImportStatePassthroughContext,
		},

		Schema: map[string]*schema.Schema{
			"name": {
				Description: "Name of the account.",
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
			},
			"email": {
				Description: "Root email of the account.",
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
			},
			"sso": {
				Description: "Assigned SSO user settings.",
				Type:        schema.TypeList,
				Required:    true,
				MaxItems:    1,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"first_name": {
							Description: "First name of the user.",
							Type:        schema.TypeString,
							Required:    true,
						},

						"last_name": {
							Description: "Last name of the user.",
							Type:        schema.TypeString,
							Required:    true,
						},

						"email": {
							Description: "Email address of the user. If you use automatic provisioning this email address should already exist in AWS SSO.",
							Type:        schema.TypeString,
							Required:    true,
						},
					},
				},
			},
			"organizational_unit": {
				Description: "Name of the Organizational Unit under which the account resides.",
				Type:        schema.TypeString,
				Required:    true,
			},
			"tags": {
				Description: "Key-value map of resource tags for the account.",
				Type:        schema.TypeMap,
				Optional:    true,
				Elem:        &schema.Schema{Type: schema.TypeString},
			},
			"provisioned_product_name": {
				Description: "Name of the service catalog product that is provisioned. Defaults to a slugified version of the account name.",
				Type:        schema.TypeString,
				Optional:    true,
				Computed:    true,
				ForceNew:    true,
			},
			"account_id": {
				Description: "ID of the AWS account.",
				Type:        schema.TypeString,
				Computed:    true,
			},
		},
	}
}

var accountMutex sync.Mutex

func resourceAWSAccountCreate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	scconn := m.(*AWSClient).scconn
	organizationsconn := m.(*AWSClient).organizationsconn

	productId, artifactId, err := findServiceCatalogAccountProductId(scconn)
	if err != nil {
		return diag.FromErr(err)
	}

	// Get the name, ou and SSO details from the config.
	name := d.Get("name").(string)
	ou := d.Get("organizational_unit").(string)
	ppn := d.Get("provisioned_product_name").(string)
	sso := d.Get("sso").([]interface{})[0].(map[string]interface{})

	// If no provisioned product name was configured, use the name.
	if ppn == "" {
		ppn = invalidProductNameChars.ReplaceAllString(name, "_")
	}

	// Create a new parameters struct.
	params := &servicecatalog.ProvisionProductInput{
		ProductId:              productId,
		ProvisionedProductName: aws.String(ppn),
		ProvisioningArtifactId: artifactId,
		ProvisioningParameters: []*servicecatalog.ProvisioningParameter{
			{
				Key:   aws.String("AccountName"),
				Value: aws.String(name),
			},
			{
				Key:   aws.String("AccountEmail"),
				Value: aws.String(d.Get("email").(string)),
			},
			{
				Key:   aws.String("SSOUserFirstName"),
				Value: aws.String(sso["first_name"].(string)),
			},
			{
				Key:   aws.String("SSOUserLastName"),
				Value: aws.String(sso["last_name"].(string)),
			},
			{
				Key:   aws.String("SSOUserEmail"),
				Value: aws.String(sso["email"].(string)),
			},
			{
				Key:   aws.String("ManagedOrganizationalUnit"),
				Value: aws.String(ou),
			},
		},
	}

	accountMutex.Lock()
	defer accountMutex.Unlock()

	account, err := scconn.ProvisionProduct(params)
	if err != nil {
		return diag.Errorf("Error provisioning account %s: %v", name, err)
	}

	// Set the ID so we can cleanup the provisioned account in case of a failure.
	d.SetId(*account.RecordDetail.ProvisionedProductId)

	// Wait for the provisioning to finish.
	record, diags := waitForProvisioning(name, account.RecordDetail.RecordId, m)
	if diags.HasError() {
		return diags
	}

	tags := d.Get("tags").(map[string]interface{})
	for _, output := range record.RecordOutputs {
		switch *output.OutputKey {
		case "AccountId":
			_, err := organizationsconn.TagResource(&organizations.TagResourceInput{
				ResourceId: output.OutputValue,
				Tags:       toOrganizationsTags(tags),
			})
			if err != nil {
				return diag.Errorf("Error tagging account %s: %v", *output.OutputValue, err)
			}
		}
	}

	return resourceAWSAccountRead(ctx, d, m)
}

func resourceAWSAccountRead(_ context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	scconn := m.(*AWSClient).scconn
	organizationsconn := m.(*AWSClient).organizationsconn

	product, err := scconn.DescribeProvisionedProduct(&servicecatalog.DescribeProvisionedProductInput{
		Id: aws.String(d.Id()),
	})
	if err != nil {
		return diag.Errorf("Error reading configuration of provisioned product: %v", err)
	}

	status, err := findLatestSuccessfulRecord(scconn, *product.ProvisionedProductDetail.Id)
	if err != nil {
		return diag.FromErr(err)
	}

	// update config
	var accountId string
	sso := map[string]interface{}{
		"first_name": "",
		"last_name":  "",
		"email":      "",
	}

	ssoConfig := d.Get("sso").([]interface{})
	if len(ssoConfig) > 0 {
		sso = ssoConfig[0].(map[string]interface{})
	}

	if err := d.Set("provisioned_product_name", *product.ProvisionedProductDetail.Name); err != nil {
		return diag.FromErr(err)
	}

	for _, output := range status.RecordOutputs {
		switch *output.OutputKey {
		case "AccountEmail":
			if err := d.Set("email", *output.OutputValue); err != nil {
				return diag.FromErr(err)
			}
		case "AccountId":
			accountId = *output.OutputValue
			if err := d.Set("account_id", *output.OutputValue); err != nil {
				return diag.FromErr(err)
			}
		case "SSOUserEmail":
			sso["email"] = *output.OutputValue
		}
	}
	if err := d.Set("sso", []interface{}{sso}); err != nil {
		return diag.FromErr(err)
	}

	// query for account name
	if accountId == "" {
		return diag.Errorf("could not find account ID in provisioned product")
	}

	account, err := organizationsconn.DescribeAccount(&organizations.DescribeAccountInput{
		AccountId: aws.String(accountId),
	})
	if err != nil {
		return diag.Errorf("error reading account information for %s: %v", accountId, err)
	}
	if err := d.Set("name", *account.Account.Name); err != nil {
		return diag.FromErr(err)
	}

	ou, err := findParentOrganizationalUnit(organizationsconn, accountId)
	if err != nil {
		return diag.FromErr(err)
	}
	if err := d.Set("organizational_unit", *ou.Name); err != nil {
		return diag.FromErr(err)
	}

	tags, err := organizationsconn.ListTagsForResource(&organizations.ListTagsForResourceInput{
		ResourceId: aws.String(accountId),
	})
	if err != nil {
		return diag.Errorf("error listing tags for resource %s: %v", accountId, err)
	}
	if err := d.Set("tags", fromOrganizationTags(tags.Tags)); err != nil {
		return diag.FromErr(err)
	}

	return nil
}

func resourceAWSAccountUpdate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	scconn := m.(*AWSClient).scconn
	organizationsconn := m.(*AWSClient).organizationsconn

	if d.HasChangeExcept("tags") {
		// Get the name, email, ou and SSO details from the config.
		name := d.Get("name").(string)
		email := d.Get("email").(string)
		ou := d.Get("organizational_unit").(string)
		sso := d.Get("sso").([]interface{})[0].(map[string]interface{})

		// Create a new parameters struct.
		params := &servicecatalog.UpdateProvisionedProductInput{
			ProvisionedProductId: aws.String(d.Id()),
			ProvisioningParameters: []*servicecatalog.UpdateProvisioningParameter{
				{
					Key:   aws.String("AccountName"),
					Value: aws.String(name),
				},
				{
					Key:   aws.String("AccountEmail"),
					Value: aws.String(email),
				},
				{
					Key:   aws.String("SSOUserFirstName"),
					Value: aws.String(sso["first_name"].(string)),
				},
				{
					Key:   aws.String("SSOUserLastName"),
					Value: aws.String(sso["last_name"].(string)),
				},
				{
					Key:   aws.String("SSOUserEmail"),
					Value: aws.String(sso["email"].(string)),
				},
				{
					Key:   aws.String("ManagedOrganizationalUnit"),
					Value: aws.String(ou),
				},
			},
		}

		accountMutex.Lock()
		defer accountMutex.Unlock()

		account, err := scconn.UpdateProvisionedProduct(params)
		if err != nil {
			return diag.Errorf("error updating provisioned account %s: %v", name, err)
		}

		// Wait for the provisioning to finish.
		_, diags := waitForProvisioning(name, account.RecordDetail.RecordId, m)
		if diags.HasError() {
			return diags
		}
	}

	if d.HasChange("tags") {
		o, n := d.GetChange("tags")
		accountId := d.Get("account_id").(string)

		if err := updateAccountTags(organizationsconn, accountId, o, n); err != nil {
			return diag.Errorf("error updating AWS Organizations Account (%s) tags: %s", accountId, err)
		}
	}

	return resourceAWSAccountRead(ctx, d, m)
}

func resourceAWSAccountDelete(_ context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	scconn := m.(*AWSClient).scconn

	// Get the name from the config.
	name := d.Get("name").(string)

	accountMutex.Lock()
	defer accountMutex.Unlock()

	account, err := scconn.TerminateProvisionedProduct(&servicecatalog.TerminateProvisionedProductInput{
		ProvisionedProductId: aws.String(d.Id()),
	})
	if err != nil {
		return diag.Errorf("error deleting provisioned account %s: %v", name, err)
	}

	// Wait for the provisioning to finish.
	_, diags := waitForProvisioning(name, account.RecordDetail.RecordId, m)
	return diags
}

// waitForProvisioning waits until the provisioning finished.
func waitForProvisioning(name string, recordID *string, m interface{}) (*servicecatalog.DescribeRecordOutput, diag.Diagnostics) {
	scconn := m.(*AWSClient).scconn

	var (
		status *servicecatalog.DescribeRecordOutput
		diags  diag.Diagnostics
	)

	record := &servicecatalog.DescribeRecordInput{
		Id: recordID,
	}

	for {
		// Get the provisioning status.
		var err error
		status, err = scconn.DescribeRecord(record)
		if err != nil {
			return status, diag.Errorf("error reading provisioning status of account %s: %v", name, err)
		}

		// If the provisioning succeeded we are done.
		if *status.RecordDetail.Status == servicecatalog.RecordStatusSucceeded {
			break
		}

		// If the provisioning failed we try to cleanup the tainted account.
		if *status.RecordDetail.Status == servicecatalog.RecordStatusFailed {
			return status, diag.Errorf("provisioning account %s failed: %s", name, *status.RecordDetail.RecordErrors[0].Description)
		}

		// Wait 5 seconds before checking the status again.
		time.Sleep(5 * time.Second)
	}

	return status, diags
}

func findServiceCatalogAccountProductId(conn *servicecatalog.ServiceCatalog) (*string, *string, error) {
	products, err := conn.SearchProducts(&servicecatalog.SearchProductsInput{
		Filters: map[string][]*string{"FullTextSearch": {aws.String("AWS Control Tower Account Factory")}},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("error occured while searching for the account product: %v", err)
	}
	if len(products.ProductViewSummaries) != 1 {
		return nil, nil, fmt.Errorf("unexpected number of search results: %d", len(products.ProductViewSummaries))
	}

	productId := products.ProductViewSummaries[0].ProductId

	artifacts, err := conn.ListProvisioningArtifacts(&servicecatalog.ListProvisioningArtifactsInput{
		ProductId: productId,
	})
	if err != nil {
		return productId, nil, fmt.Errorf("error listing provisioning artifacts: %v", err)
	}

	// Try to find the active (which should be the latest) artifact.
	var artifactID *string
	for _, artifact := range artifacts.ProvisioningArtifactDetails {
		if *artifact.Active {
			artifactID = artifact.Id
			break
		}
	}
	if artifactID == nil {
		return productId, nil, fmt.Errorf("could not find the provisioning artifact ID")
	}

	return productId, artifactID, nil
}

func findLatestSuccessfulRecord(conn *servicecatalog.ServiceCatalog, provisionedProductId string) (*servicecatalog.DescribeRecordOutput, error) {
	records, err := conn.ListRecordHistory(&servicecatalog.ListRecordHistoryInput{
		SearchFilter: &servicecatalog.ListRecordHistorySearchFilter{
			Key:   aws.String("provisionedproduct"),
			Value: aws.String(provisionedProductId),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("error querying the record history of provisioned product: %v", err)
	}

	var latestSuccessfulRecordId string
	for _, v := range records.RecordDetails {
		if *v.Status == servicecatalog.RecordStatusSucceeded {
			latestSuccessfulRecordId = *v.RecordId
			break
		}
	}
	if latestSuccessfulRecordId == "" {
		return nil, fmt.Errorf("no successful record found for provisioned product")
	}

	status, err := conn.DescribeRecord(&servicecatalog.DescribeRecordInput{
		Id: aws.String(latestSuccessfulRecordId),
	})
	if err != nil {
		return nil, fmt.Errorf("error reading configuration of provisioned product: %v", err)
	}

	return status, nil
}

func findParentOrganizationalUnit(conn *organizations.Organizations, identifier string) (*organizations.OrganizationalUnit, error) {
	parents, err := conn.ListParents(&organizations.ListParentsInput{
		ChildId: aws.String(identifier),
	})
	if err != nil {
		return nil, fmt.Errorf("error reading parents for %s: %v", identifier, err)
	}

	var parentOuId string
	for _, v := range parents.Parents {
		if *v.Type == organizations.ParentTypeOrganizationalUnit {
			parentOuId = *v.Id
			break
		}
	}
	if parentOuId == "" {
		return nil, fmt.Errorf("no OU parent found for %s", identifier)
	}

	ou, err := conn.DescribeOrganizationalUnit(&organizations.DescribeOrganizationalUnitInput{
		OrganizationalUnitId: aws.String(parentOuId),
	})
	if err != nil {
		return nil, fmt.Errorf("error describing parent OU %s: %v", parentOuId, err)
	}

	return ou.OrganizationalUnit, nil
}

func toOrganizationsTags(tags map[string]interface{}) []*organizations.Tag {
	result := make([]*organizations.Tag, 0, len(tags))

	for k, v := range tags {
		tag := &organizations.Tag{
			Key:   aws.String(k),
			Value: aws.String(v.(string)),
		}

		result = append(result, tag)
	}

	return result
}

func fromOrganizationTags(tags []*organizations.Tag) map[string]*string {
	m := make(map[string]*string, len(tags))

	for _, tag := range tags {
		m[aws.StringValue(tag.Key)] = tag.Value
	}

	return m
}

func updateAccountTags(conn *organizations.Organizations, identifier string, oldTags interface{}, newTags interface{}) error {
	oldTagsMap := oldTags.(map[string]interface{})
	newTagsMap := newTags.(map[string]interface{})

	if removedTags := removedTags(oldTagsMap, newTagsMap); len(removedTags) > 0 {
		input := &organizations.UntagResourceInput{
			ResourceId: aws.String(identifier),
			TagKeys:    aws.StringSlice(keys(removedTags)),
		}

		_, err := conn.UntagResource(input)

		if err != nil {
			return fmt.Errorf("error untagging resource (%s): %w", identifier, err)
		}
	}

	if updatedTags := updatedTags(oldTagsMap, newTagsMap); len(updatedTags) > 0 {
		input := &organizations.TagResourceInput{
			ResourceId: aws.String(identifier),
			Tags:       toOrganizationsTags(updatedTags),
		}

		_, err := conn.TagResource(input)

		if err != nil {
			return fmt.Errorf("error tagging resource (%s): %w", identifier, err)
		}
	}

	return nil
}

func removedTags(oldTagsMap map[string]interface{}, newTagsMap map[string]interface{}) map[string]interface{} {
	result := map[string]interface{}{}

	for k, v := range oldTagsMap {
		if _, ok := newTagsMap[k]; !ok {
			result[k] = v
		}
	}

	return result
}

func updatedTags(oldTagsMap map[string]interface{}, newTagsMap map[string]interface{}) map[string]interface{} {
	result := map[string]interface{}{}

	for k, newV := range newTagsMap {
		if oldV, ok := oldTagsMap[k]; !ok || oldV != newV {
			result[k] = newV
		}
	}

	return result
}

func keys(value map[string]interface{}) []string {
	keys := make([]string, 0, len(value))
	for k := range value {
		keys = append(keys, k)
	}

	return keys
}
