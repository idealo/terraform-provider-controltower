package provider

import (
	"context"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/organizations"
	"github.com/aws/aws-sdk-go/service/servicecatalog"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
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
						"firstname": {
							Description: "First name of the user.",
							Type:        schema.TypeString,
							Required:    true,
						},

						"lastname": {
							Description: "Last name of the user.",
							Type:        schema.TypeString,
							Required:    true,
						},

						"email": {
							Description: "Email address of the user.",
							Type:        schema.TypeString,
							Required:    true,
						},
					},
				},
			},
			"organizational_unit": {
				Description: "Organizational Unit under which the account resides.",
				Type:        schema.TypeString,
				Required:    true,
			},
			"provisioned_product_name": {
				Description: "Name of the service catalog product that is provisioned.",
				Type:        schema.TypeString,
				Optional:    true,
				Computed:    true,
				ForceNew:    true,
			},
			"account_id": {
				Description: "ID of the AWS account",
				Type:        schema.TypeString,
				Computed:    true,
			},
		},
	}
}

var accountMutex sync.Mutex

func resourceAWSAccountCreate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	scconn := m.(*AWSClient).scconn

	products, err := scconn.SearchProducts(&servicecatalog.SearchProductsInput{
		Filters: map[string][]*string{"FullTextSearch": {aws.String("AWS Control Tower Account Factory")}},
	})
	if err != nil {
		return diag.FromErr(err)
	}
	if len(products.ProductViewSummaries) != 1 {
		return diag.Errorf("Unexpected number of search results: %d", len(products.ProductViewSummaries))
	}

	artifacts, err := scconn.ListProvisioningArtifacts(&servicecatalog.ListProvisioningArtifactsInput{
		ProductId: products.ProductViewSummaries[0].ProductId,
	})
	if err != nil {
		return diag.Errorf("Error listing provisioning artifacts: %v", err)
	}

	// Try to find the active (which should be the latest) artifact.
	artifactID := ""
	for _, artifact := range artifacts.ProvisioningArtifactDetails {
		if *artifact.Active {
			artifactID = *artifact.Id
			break
		}
	}
	if artifactID == "" {
		return diag.Errorf("Could not find the provisioning artifact ID")
	}

	// Get the name, ou and SSO details from the config.
	name := d.Get("name").(string)
	ou := d.Get("organizational_unit").(string)
	ppn := d.Get("provisioned_product_name").(string)
	sso := d.Get("sso").([]interface{})[0].(map[string]interface{})

	// If no provisioned product name was configured, use the name.
	if ppn == "" {
		ppn = name
	}

	// Create a new parameters struct.
	params := &servicecatalog.ProvisionProductInput{
		ProductId:              products.ProductViewSummaries[0].ProductId,
		ProvisionedProductName: aws.String(ppn),
		ProvisioningArtifactId: aws.String(artifactID),
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
				Value: aws.String(sso["firstname"].(string)),
			},
			{
				Key:   aws.String("SSOUserLastName"),
				Value: aws.String(sso["lastname"].(string)),
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
	diags := waitForProvisioning(name, account.RecordDetail.RecordId, m)
	if diags.HasError() {
		return diags
	}

	return resourceAWSAccountRead(ctx, d, m)
}

func resourceAWSAccountRead(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	scconn := m.(*AWSClient).scconn
	organizationsconn := m.(*AWSClient).organizationsconn

	product, err := scconn.DescribeProvisionedProduct(&servicecatalog.DescribeProvisionedProductInput{
		Id: aws.String(d.Id()),
	})
	if err != nil {
		return diag.Errorf("Error reading configuration of provisioned product: %v", err)
	}

	records, err := scconn.ListRecordHistory(&servicecatalog.ListRecordHistoryInput{
		SearchFilter: &servicecatalog.ListRecordHistorySearchFilter{
			Key:   aws.String("provisionedproduct"),
			Value: product.ProvisionedProductDetail.Id,
		},
	})
	if err != nil {
		return diag.Errorf("Error querying the record history of provisioned product: %v", err)
	}

	var latestSuccessfulRecordId string
	for _, v := range records.RecordDetails {
		if *v.Status == servicecatalog.RecordStatusSucceeded {
			latestSuccessfulRecordId = *v.RecordId
			break
		}
	}
	if latestSuccessfulRecordId == "" {
		return diag.Errorf("No successful record found for provisioned product")
	}

	record := &servicecatalog.DescribeRecordInput{
		Id: aws.String(latestSuccessfulRecordId),
	}

	status, err := scconn.DescribeRecord(record)
	if err != nil {
		return diag.Errorf("Error reading configuration of provisioned product: %v", err)
	}

	// update config
	var accountId string
	sso := map[string]interface{}{
		"firstname": "",
		"lastname":  "",
		"email":     "",
	}

	ssoConfig := d.Get("sso").([]interface{})
	if len(ssoConfig) > 0 {
		sso = ssoConfig[0].(map[string]interface{})
	}

	d.Set("provisioned_product_name", *product.ProvisionedProductDetail.Name)
	for _, output := range status.RecordOutputs {
		switch *output.OutputKey {
		case "AccountEmail":
			d.Set("email", *output.OutputValue)
		case "AccountId":
			accountId = *output.OutputValue
			d.Set("account_id", *output.OutputValue)
		case "SSOUserEmail":
			sso["email"] = *output.OutputValue
		}
	}
	if err := d.Set("sso", []interface{}{sso}); err != nil {
		return diag.FromErr(err)
	}

	// query for account name
	if accountId == "" {
		return diag.Errorf("Could not find account ID in provisioned product")
	}

	account, err := organizationsconn.DescribeAccount(&organizations.DescribeAccountInput{
		AccountId: aws.String(accountId),
	})
	if err != nil {
		return diag.Errorf("Error reading account information for %s: %v", accountId, err)
	}
	if err := d.Set("name", *account.Account.Name); err != nil {
		return diag.FromErr(err)
	}

	parents, err := organizationsconn.ListParents(&organizations.ListParentsInput{
		ChildId: aws.String(accountId),
	})
	if err != nil {
		return diag.Errorf("Error reading parents for %s: %v", accountId, err)
	}

	var parentOuId string
	for _, v := range parents.Parents {
		if *v.Type == "ORGANIZATIONAL_UNIT" {
			parentOuId = *v.Id
			break
		}
	}
	if parentOuId == "" {
		return diag.Errorf("No OU parent found for %s", accountId)
	}

	ou, err := organizationsconn.DescribeOrganizationalUnit(&organizations.DescribeOrganizationalUnitInput{
		OrganizationalUnitId: aws.String(parentOuId),
	})
	if err != nil {
		return diag.Errorf("Error describing parent OU %s: %v", parentOuId, err)
	}
	if err := d.Set("organizational_unit", *ou.OrganizationalUnit.Name); err != nil {
		return diag.FromErr(err)
	}

	return nil
}

func resourceAWSAccountUpdate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	scconn := m.(*AWSClient).scconn

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
				Value: aws.String(sso["firstname"].(string)),
			},
			{
				Key:   aws.String("SSOUserLastName"),
				Value: aws.String(sso["lastname"].(string)),
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
		return diag.Errorf("Error updating provisioned account %s: %v", name, err)
	}

	// Wait for the provisioning to finish.
	diags := waitForProvisioning(name, account.RecordDetail.RecordId, m)
	if diags.HasError() {
		return diags
	}

	return resourceAWSAccountRead(ctx, d, m)
}

func resourceAWSAccountDelete(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	scconn := m.(*AWSClient).scconn

	// Get the name from the config.
	name := d.Get("name").(string)

	accountMutex.Lock()
	defer accountMutex.Unlock()

	account, err := scconn.TerminateProvisionedProduct(&servicecatalog.TerminateProvisionedProductInput{
		ProvisionedProductId: aws.String(d.Id()),
	})
	if err != nil {
		return diag.Errorf("Error deleting provisioned account %s: %v", name, err)
	}

	// Wait for the provisioning to finish.
	return waitForProvisioning(name, account.RecordDetail.RecordId, m)
}

// waitForProvisioning waits until the provisioning finished.
func waitForProvisioning(name string, recordID *string, m interface{}) diag.Diagnostics {
	scconn := m.(*AWSClient).scconn

	var diags diag.Diagnostics

	record := &servicecatalog.DescribeRecordInput{
		Id: recordID,
	}

	for {
		// Get the provisioning status.
		status, err := scconn.DescribeRecord(record)
		if err != nil {
			return diag.Errorf("Error reading provisioning status of account %s: %v", name, err)
		}

		// If the provisioning succeeded we are done.
		if *status.RecordDetail.Status == servicecatalog.RecordStatusSucceeded {
			break
		}

		// If the provisioning failed we try to cleanup the tainted account.
		if *status.RecordDetail.Status == servicecatalog.RecordStatusFailed {
			return diag.Errorf("Provisioning account %s failed: %s", name, *status.RecordDetail.RecordErrors[0].Description)
		}

		// Wait 5 seconds before checking the status again.
		time.Sleep(5 * time.Second)
	}

	return diags
}
