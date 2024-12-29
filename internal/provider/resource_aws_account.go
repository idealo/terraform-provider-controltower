package provider

import (
	"context"
	"errors"
	"fmt"
	orgTypes "github.com/aws/aws-sdk-go-v2/service/organizations/types"
	scTypes "github.com/aws/aws-sdk-go-v2/service/servicecatalog/types"
	"regexp"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	"github.com/aws/aws-sdk-go-v2/service/servicecatalog"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
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
				Description:  "Name of the account.",
				Type:         schema.TypeString,
				Required:     true,
				ForceNew:     true,
				ValidateFunc: validation.StringMatch(regexp.MustCompile(`^[ -~]+$`), "must only contain characters between char code 32 (SPACE) and 126 (TILDE)"),
			},
			"email": {
				Description:  "Root email of the account.",
				Type:         schema.TypeString,
				Required:     true,
				ForceNew:     true,
				ValidateFunc: validateEmailAddress,
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
							Description:  "Email address of the user. If you use automatic provisioning this email address should already exist in AWS SSO.",
							Type:         schema.TypeString,
							Required:     true,
							ValidateFunc: validateEmailAddress,
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
			"path_id": {
				Description:  "Name of the path identifier of the product. This value is optional if the product has a default path, and required if the product has more than one path. To list the paths for a product, use ListLaunchPaths.",
				Type:         schema.TypeString,
				Optional:     true,
				Computed:     true,
				ForceNew:     true,
				ValidateFunc: validation.StringMatch(regexp.MustCompile(`^[a-zA-Z0-9_-]*$`), "must only contain alphanumeric characters, underscores and hyphens"),
			},
			"provisioned_product_name": {
				Description:  "Name of the service catalog product that is provisioned. Defaults to a slugified version of the account name.",
				Type:         schema.TypeString,
				Optional:     true,
				Computed:     true,
				ForceNew:     true,
				ValidateFunc: validation.StringMatch(regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.^-]*$`), "must only contain alphanumeric characters, dots, underscores and hyphens"),
			},
			"organizational_unit_id_on_delete": {
				Description:  "ID of the Organizational Unit to which the account should be moved when the resource is deleted. If no value is provided, the account will not be moved.",
				Type:         schema.TypeString,
				Optional:     true,
				ValidateFunc: validation.StringMatch(regexp.MustCompile("^ou-[0-9a-z]{4,32}-[a-z0-9]{8,32}$"), "see https://docs.aws.amazon.com/organizations/latest/APIReference/API_MoveAccount.html#organizations-MoveAccount-request-DestinationParentId"),
			},
			"close_account_on_delete": {
				Description: "If enabled, this will close the AWS account on resource deletion, beginning the 90-day suspension period. Otherwise, the account will just be unenrolled from Control Tower.",
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false,
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

	cfg := m.(aws.Config)

	scconn := servicecatalog.NewFromConfig(cfg)
	organizationsconn := organizations.NewFromConfig(cfg)

	productId, artifactId, err := findServiceCatalogAccountProductId(ctx, scconn)
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
	//migrate this to v2

	params := &servicecatalog.ProvisionProductInput{
		ProductId:              productId,
		ProvisionedProductName: aws.String(ppn),
		ProvisioningArtifactId: artifactId,
		ProvisioningParameters: []scTypes.ProvisioningParameter{
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

	// Optionally add the path id.
	if v, ok := d.GetOk("path_id"); ok {
		params.PathId = aws.String(v.(string))
	}

	accountMutex.Lock()
	defer accountMutex.Unlock()

	account, err := scconn.ProvisionProduct(ctx, params)
	if err != nil {
		return diag.Errorf("error provisioning account %s: %v", name, err)
	}

	// Set the ID so we can cleanup the provisioned account in case of a failure.
	d.SetId(*account.RecordDetail.ProvisionedProductId)

	// Wait for the provisioning to finish.
	record, diags := waitForProvisioning(ctx, name, account.RecordDetail.RecordId, scconn)
	if diags.HasError() {
		return diags
	}

	tags := d.Get("tags").(map[string]interface{})
	for _, output := range record.RecordOutputs {
		switch *output.OutputKey {
		case "AccountId":
			_, err := organizationsconn.TagResource(ctx, &organizations.TagResourceInput{
				ResourceId: output.OutputValue,
				Tags:       toOrganizationsTags(tags),
			})
			if err != nil {
				return diag.Errorf("error tagging account %s: %v", *output.OutputValue, err)
			}
		}
	}

	return resourceAWSAccountRead(ctx, d, m)
}

func resourceAWSAccountRead(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	cfg := m.(aws.Config)

	scconn := servicecatalog.NewFromConfig(cfg)
	organizationsconn := organizations.NewFromConfig(cfg)

	product, err := scconn.DescribeProvisionedProduct(ctx, &servicecatalog.DescribeProvisionedProductInput{
		Id: aws.String(d.Id()),
	})

	if !d.IsNewResource() {
		var notFoundErr *scTypes.ResourceNotFoundException
		if errors.As(err, &notFoundErr) {
			d.SetId("")
			return nil
		}
	}
	if err != nil {
		return diag.Errorf("error reading configuration of provisioned product: %v", err)
	}

	lastRecordId := product.ProvisionedProductDetail.LastProvisioningRecordId
	if product.ProvisionedProductDetail.LastSuccessfulProvisioningRecordId != nil {
		lastRecordId = product.ProvisionedProductDetail.LastSuccessfulProvisioningRecordId
	}
	status, err := scconn.DescribeRecord(ctx, &servicecatalog.DescribeRecordInput{
		Id: lastRecordId,
	})
	if err != nil {
		return diag.Errorf("error reading last successful record of provisioned product: %v", err)
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

	if err = d.Set("path_id", *status.RecordDetail.PathId); err != nil {
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

	// exit read if no account id is found in the product
	if accountId == "" {
		return nil
	}

	account, err := organizationsconn.DescribeAccount(ctx, &organizations.DescribeAccountInput{
		AccountId: aws.String(accountId),
	})
	if err != nil {
		return diag.Errorf("error reading account information for %s: %v", accountId, err)
	}
	if err := d.Set("name", *account.Account.Name); err != nil {
		return diag.FromErr(err)
	}

	ou, err := findParentOrganizationalUnit(ctx, organizationsconn, accountId)
	if err != nil {
		return diag.FromErr(err)
	}
	if err := d.Set("organizational_unit", *ou.Name); err != nil {
		return diag.FromErr(err)
	}

	tags, err := organizationsconn.ListTagsForResource(ctx, &organizations.ListTagsForResourceInput{
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
	cfg := m.(aws.Config)

	scconn := servicecatalog.NewFromConfig(cfg)
	organizationsconn := organizations.NewFromConfig(cfg)

	if d.HasChangesExcept("tags", "organizational_unit_id_on_delete", "close_account_on_delete") {
		productId, artifactId, err := findServiceCatalogAccountProductId(ctx, scconn)
		if err != nil {
			return diag.FromErr(err)
		}

		// Get the name, email, ou and SSO details from the config.
		name := d.Get("name").(string)
		email := d.Get("email").(string)
		ou := d.Get("organizational_unit").(string)
		sso := d.Get("sso").([]interface{})[0].(map[string]interface{})

		// Create a new parameters struct.
		params := &servicecatalog.UpdateProvisionedProductInput{
			ProvisionedProductId:   aws.String(d.Id()),
			ProductId:              productId,
			ProvisioningArtifactId: artifactId,
			ProvisioningParameters: []scTypes.UpdateProvisioningParameter{
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

		// Optionally add the path id.
		if pathIdConfig := d.GetRawConfig().GetAttr("path_id"); !pathIdConfig.IsNull() {
			params.PathId = aws.String(pathIdConfig.AsString())
		}

		accountMutex.Lock()
		defer accountMutex.Unlock()

		account, err := scconn.UpdateProvisionedProduct(ctx, params)
		if err != nil {
			return diag.Errorf("error updating provisioned account %s: %v", name, err)
		}

		// Wait for the provisioning to finish.
		_, diags := waitForProvisioning(ctx, name, account.RecordDetail.RecordId, scconn)
		if diags.HasError() {
			return diags
		}
	}

	if d.HasChange("tags") {
		o, n := d.GetChange("tags")
		accountId := d.Get("account_id").(string)

		if err := updateAccountTags(ctx, organizationsconn, accountId, o, n); err != nil {
			return diag.Errorf("error updating AWS Organizations Account (%s) tags: %s", accountId, err)
		}
	}

	return resourceAWSAccountRead(ctx, d, m)
}

func resourceAWSAccountDelete(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	cfg := m.(aws.Config)

	scconn := servicecatalog.NewFromConfig(cfg)
	organizationsconn := organizations.NewFromConfig(cfg)

	name := d.Get("name").(string)

	product, err := scconn.DescribeProvisionedProduct(ctx, &servicecatalog.DescribeProvisionedProductInput{
		Id: aws.String(d.Id()),
	})

	if err != nil {
		return diag.Errorf("error describing provisioned product: %s", err)
	}

	accountMutex.Lock()
	defer accountMutex.Unlock()

	account, err := scconn.TerminateProvisionedProduct(ctx, &servicecatalog.TerminateProvisionedProductInput{
		ProvisionedProductId: aws.String(d.Id()),
	})
	if err != nil {
		return diag.Errorf("error deleting provisioned account %s: %s", name, err)
	}

	// Wait for the provisioning to finish.
	_, diags := waitForProvisioning(ctx, name, account.RecordDetail.RecordId, scconn)
	if diags.HasError() {
		return diags
	}

	accountId, accountExists := d.GetOk("account_id")
	accountProvisioned := product.ProvisionedProductDetail.LastSuccessfulProvisioningRecordId != nil
	if newOuId, ok := d.GetOk("organizational_unit_id_on_delete"); ok && accountExists && accountProvisioned {
		rootId, err := findParentOrganizationRootId(ctx, organizationsconn, accountId.(string))
		if err != nil {
			return diag.FromErr(err)
		}

		_, err = organizationsconn.MoveAccount(ctx, &organizations.MoveAccountInput{
			AccountId:           aws.String(accountId.(string)),
			SourceParentId:      aws.String(rootId),
			DestinationParentId: aws.String(newOuId.(string)),
		})
		if err != nil {
			return diag.FromErr(err)
		}
	}

	closeAccount := d.Get("close_account_on_delete").(bool)
	if closeAccount && accountExists && accountProvisioned {
		_, err := organizationsconn.CloseAccount(ctx, &organizations.CloseAccountInput{
			AccountId: aws.String(accountId.(string)),
		})
		if err != nil {
			return diag.Errorf("error closing account %s: %s", accountId, err)
		}
	}

	return nil
}

// waitForProvisioning waits until the provisioning finished.
func waitForProvisioning(ctx context.Context, name string, recordID *string, client *servicecatalog.Client) (*servicecatalog.DescribeRecordOutput, diag.Diagnostics) {
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
		status, err = client.DescribeRecord(ctx, record)
		if err != nil {
			return status, diag.Errorf("error reading provisioning status of account %s: %s", name, err)
		}

		// If the provisioning succeeded we are done.
		if status.RecordDetail.Status == scTypes.RecordStatusSucceeded {
			break
		}

		// If the provisioning failed we try to cleanup the tainted account.
		if status.RecordDetail.Status == scTypes.RecordStatusFailed {
			if len(status.RecordDetail.RecordErrors) > 0 {
				return status, diag.Errorf("provisioning account %s failed: %s", name, *status.RecordDetail.RecordErrors[0].Description)
			}
			return status, diag.Errorf("provisioning account %s failed with unknown error", name)
		}

		// Wait 5 seconds before checking the status again.
		time.Sleep(5 * time.Second)
	}

	return status, diags
}

func findServiceCatalogAccountProductId(ctx context.Context, client *servicecatalog.Client) (*string, *string, error) {
	products, err := client.SearchProducts(ctx, &servicecatalog.SearchProductsInput{
		Filters: map[string][]string{"FullTextSearch": {"AWS Control Tower Account Factory"}},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("error occurred while searching for the account product: %w", err)
	}
	if len(products.ProductViewSummaries) != 1 {
		return nil, nil, fmt.Errorf("unexpected number of search results: %d", len(products.ProductViewSummaries))
	}

	productId := products.ProductViewSummaries[0].ProductId

	artifacts, err := client.ListProvisioningArtifacts(ctx, &servicecatalog.ListProvisioningArtifactsInput{
		ProductId: productId,
	})
	if err != nil {
		return productId, nil, fmt.Errorf("error listing provisioning artifacts: %w", err)
	}

	// Try to find the active (which should be the latest) artifact.
	var artifactID *string
	for _, artifact := range artifacts.ProvisioningArtifactDetails {
		if artifact.Active != nil && *artifact.Active {
			artifactID = artifact.Id
			break
		}
	}
	if artifactID == nil {
		return productId, nil, fmt.Errorf("could not find the provisioning artifact ID")
	}

	return productId, artifactID, nil
}
func findParentOrganizationalUnit(ctx context.Context, client *organizations.Client, identifier string) (*orgTypes.OrganizationalUnit, error) {
	paginator := organizations.NewListParentsPaginator(client, &organizations.ListParentsInput{
		ChildId: aws.String(identifier),
	})

	var parentOuId string
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("error reading parents for %s: %w", identifier, err)
		}

		for _, parent := range output.Parents {
			if parent.Type == orgTypes.ParentTypeOrganizationalUnit {
				parentOuId = *parent.Id
				break
			}
		}
		if parentOuId != "" {
			break
		}
	}

	if parentOuId == "" {
		return nil, fmt.Errorf("no OU parent found for %s", identifier)
	}

	ouOutput, err := client.DescribeOrganizationalUnit(ctx, &organizations.DescribeOrganizationalUnitInput{
		OrganizationalUnitId: aws.String(parentOuId),
	})
	if err != nil {
		return nil, fmt.Errorf("error describing parent OU %s: %w", parentOuId, err)
	}

	return ouOutput.OrganizationalUnit, nil
}

func findParentOrganizationRootId(ctx context.Context, client *organizations.Client, identifier string) (string, error) {
	paginator := organizations.NewListParentsPaginator(client, &organizations.ListParentsInput{
		ChildId: aws.String(identifier),
	})

	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return "", fmt.Errorf("error reading parents for %s: %w", identifier, err)
		}

		for _, parent := range output.Parents {
			if parent.Type == orgTypes.ParentTypeRoot {
				return *parent.Id, nil
			}
		}
	}

	return "", fmt.Errorf("no organization root parent found for %s", identifier)
}

func toOrganizationsTags(tags map[string]interface{}) []orgTypes.Tag {
	result := make([]orgTypes.Tag, 0, len(tags))

	for k, v := range tags {
		tag := &orgTypes.Tag{
			Key:   aws.String(k),
			Value: aws.String(v.(string)),
		}

		result = append(result, *tag)
	}

	return result
}

func fromOrganizationTags(tags []orgTypes.Tag) map[string]*string {
	m := make(map[string]*string, len(tags))

	for _, tag := range tags {
		m[*tag.Key] = tag.Value
	}

	return m
}

func updateAccountTags(ctx context.Context, client *organizations.Client, identifier string, oldTags interface{}, newTags interface{}) error {
	oldTagsMap := oldTags.(map[string]interface{})
	newTagsMap := newTags.(map[string]interface{})

	if removedTags := removedTags(oldTagsMap, newTagsMap); len(removedTags) > 0 {
		input := &organizations.UntagResourceInput{
			ResourceId: aws.String(identifier),
			TagKeys:    keys(removedTags),
		}

		_, err := client.UntagResource(ctx, input)

		if err != nil {
			return fmt.Errorf("error untagging resource (%s): %w", identifier, err)
		}
	}

	if updatedTags := updatedTags(oldTagsMap, newTagsMap); len(updatedTags) > 0 {
		input := &organizations.TagResourceInput{
			ResourceId: aws.String(identifier),
			Tags:       toOrganizationsTags(updatedTags),
		}

		_, err := client.TagResource(ctx, input)

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
