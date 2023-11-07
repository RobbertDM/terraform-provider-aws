// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package apprunner

import (
	"context"
	"fmt"
	"log"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/apprunner"
	"github.com/aws/aws-sdk-go-v2/service/apprunner/types"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
	"github.com/hashicorp/terraform-provider-aws/internal/conns"
	"github.com/hashicorp/terraform-provider-aws/internal/errs"
	"github.com/hashicorp/terraform-provider-aws/internal/verify"
)

// @SDKResource("aws_apprunner_custom_domain_association")
func ResourceCustomDomainAssociation() *schema.Resource {
	return &schema.Resource{
		CreateWithoutTimeout: resourceCustomDomainAssociationCreate,
		ReadWithoutTimeout:   resourceCustomDomainAssociationRead,
		DeleteWithoutTimeout: resourceCustomDomainAssociationDelete,

		Importer: &schema.ResourceImporter{
			StateContext: schema.ImportStatePassthroughContext,
		},

		Schema: map[string]*schema.Schema{
			"certificate_validation_records": {
				Type:     schema.TypeSet,
				Computed: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"name": {
							Type:     schema.TypeString,
							Computed: true,
						},
						"status": {
							Type:     schema.TypeString,
							Computed: true,
						},
						"type": {
							Type:     schema.TypeString,
							Computed: true,
						},
						"value": {
							Type:     schema.TypeString,
							Computed: true,
						},
					},
				},
			},
			"dns_target": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"domain_name": {
				Type:         schema.TypeString,
				Required:     true,
				ForceNew:     true,
				ValidateFunc: validation.StringLenBetween(1, 255),
			},
			"enable_www_subdomain": {
				Type:     schema.TypeBool,
				Optional: true,
				Default:  true,
				ForceNew: true,
			},
			"service_arn": {
				Type:         schema.TypeString,
				Required:     true,
				ForceNew:     true,
				ValidateFunc: verify.ValidARN,
			},
			"status": {
				Type:     schema.TypeString,
				Computed: true,
			},
		},
	}
}

func resourceCustomDomainAssociationCreate(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	conn := meta.(*conns.AWSClient).AppRunnerClient(ctx)

	domainName := d.Get("domain_name").(string)
	serviceArn := d.Get("service_arn").(string)

	input := &apprunner.AssociateCustomDomainInput{
		DomainName:         aws.String(domainName),
		EnableWWWSubdomain: aws.Bool(d.Get("enable_www_subdomain").(bool)),
		ServiceArn:         aws.String(serviceArn),
	}

	output, err := conn.AssociateCustomDomain(ctx, input)

	if err != nil {
		return diag.Errorf("associating App Runner Custom Domain (%s) for Service (%s): %s", domainName, serviceArn, err)
	}

	if output == nil {
		return diag.Errorf("associating App Runner Custom Domain (%s) for Service (%s): empty output", domainName, serviceArn)
	}

	d.SetId(fmt.Sprintf("%s,%s", aws.ToString(output.CustomDomain.DomainName), aws.ToString(output.ServiceArn)))
	d.Set("dns_target", output.DNSTarget)

	if err := WaitCustomDomainAssociationCreated(ctx, conn, domainName, serviceArn); err != nil {
		return diag.Errorf("waiting for App Runner Custom Domain Association (%s) creation: %s", d.Id(), err)
	}

	return resourceCustomDomainAssociationRead(ctx, d, meta)
}

func resourceCustomDomainAssociationRead(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	conn := meta.(*conns.AWSClient).AppRunnerClient(ctx)

	domainName, serviceArn, err := CustomDomainAssociationParseID(d.Id())

	if err != nil {
		return diag.FromErr(err)
	}

	customDomain, err := FindCustomDomain(ctx, conn, domainName, serviceArn)

	if !d.IsNewResource() && errs.IsA[*types.ResourceNotFoundException](err) {
		log.Printf("[WARN] App Runner Custom Domain Association (%s) not found, removing from state", d.Id())
		d.SetId("")
		return nil
	}

	if customDomain == nil {
		if d.IsNewResource() {
			return diag.Errorf("reading App Runner Custom Domain Association (%s): empty output after creation", d.Id())
		}
		log.Printf("[WARN] App Runner Custom Domain Association (%s) not found, removing from state", d.Id())
		d.SetId("")
		return nil
	}

	if err := d.Set("certificate_validation_records", flattenCustomDomainCertificateValidationRecords(customDomain.CertificateValidationRecords)); err != nil {
		return diag.Errorf("setting certificate_validation_records: %s", err)
	}

	d.Set("domain_name", customDomain.DomainName)
	d.Set("enable_www_subdomain", customDomain.EnableWWWSubdomain)
	d.Set("service_arn", serviceArn)
	d.Set("status", customDomain.Status)

	return nil
}

func resourceCustomDomainAssociationDelete(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	conn := meta.(*conns.AWSClient).AppRunnerClient(ctx)

	domainName, serviceArn, err := CustomDomainAssociationParseID(d.Id())

	if err != nil {
		return diag.FromErr(err)
	}

	input := &apprunner.DisassociateCustomDomainInput{
		DomainName: aws.String(domainName),
		ServiceArn: aws.String(serviceArn),
	}

	_, err = conn.DisassociateCustomDomain(ctx, input)

	if errs.IsA[*types.ResourceNotFoundException](err) {
		return nil
	}

	if err != nil {
		return diag.Errorf("disassociating App Runner Custom Domain (%s) for Service (%s): %s", domainName, serviceArn, err)
	}

	if err := WaitCustomDomainAssociationDeleted(ctx, conn, domainName, serviceArn); err != nil {
		if errs.IsA[*types.ResourceNotFoundException](err) {
			return nil
		}

		return diag.Errorf("waiting for App Runner Custom Domain Association (%s) deletion: %s", d.Id(), err)
	}

	return nil
}

func flattenCustomDomainCertificateValidationRecords(records []types.CertificateValidationRecord) []interface{} {
	var results []interface{}

	for _, record := range records {
		m := map[string]interface{}{
			"name":   aws.ToString(record.Name),
			"status": string(record.Status),
			"type":   aws.ToString(record.Type),
			"value":  aws.ToString(record.Value),
		}

		results = append(results, m)
	}

	return results
}
