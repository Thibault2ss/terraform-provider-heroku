package heroku

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"os"
	"strings"

	"github.com/hashicorp/terraform/helper/schema"
	"github.com/heroku/heroku-go/v3"
)

func resourceHerokuSlug() *schema.Resource {
	return &schema.Resource{
		Create: resourceHerokuSlugCreate,
		Read:   resourceHerokuSlugRead,
		Delete: resourceHerokuSlugDelete,

		Importer: &schema.ResourceImporter{
			State: resourceHerokuSlugImport,
		},

		Schema: map[string]*schema.Schema{
			"app": {
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},

			// Local tarball to be uploaded after slug creation
			"file_path": {
				Type:     schema.TypeString,
				Optional: true,
				ForceNew: true,
			},

			"blob": {
				Type:     schema.TypeList,
				Computed: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"method": {
							Type:     schema.TypeString,
							Computed: true,
						},

						"url": {
							Type:     schema.TypeString,
							Computed: true,
						},
					},
				},
			},

			"buildpack_provided_description": {
				Type:     schema.TypeString,
				Optional: true,
				ForceNew: true,
			},

			"checksum": {
				Type:     schema.TypeString,
				Optional: true,
				ForceNew: true,
			},

			"commit": {
				Type:     schema.TypeString,
				Optional: true,
				ForceNew: true,
			},

			"commit_description": {
				Type:     schema.TypeString,
				Optional: true,
				ForceNew: true,
			},

			"process_types": {
				Type:     schema.TypeList,
				Required: true,
				ForceNew: true,
				Elem: &schema.Schema{
					Type: schema.TypeMap,
				},
			},

			"size": {
				Type:     schema.TypeInt,
				Computed: true,
			},

			// Create argument; equivalent value as `stack_name`
			"stack": {
				Type:     schema.TypeString,
				Optional: true,
				ForceNew: true,
			},

			"stack_id": {
				Type:     schema.TypeString,
				Computed: true,
			},

			// Read attribute; equivalent value as `stack`
			"stack_name": {
				Type:     schema.TypeString,
				Computed: true,
			},
		},
	}
}

func resourceHerokuSlugImport(d *schema.ResourceData, meta interface{}) ([]*schema.ResourceData, error) {
	client := meta.(*heroku.Service)

	app, slugID := parseCompositeID(d.Id())

	slug, err := client.SlugInfo(context.Background(), app, slugID)
	if err != nil {
		return nil, err
	}

	d.SetId(slug.ID)
	d.Set("app", app)
	setState(d, slug)

	return []*schema.ResourceData{d}, nil
}

func resourceHerokuSlugCreate(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*heroku.Service)

	app := d.Get("app").(string)

	// Build up our creation options
	opts := heroku.SlugCreateOpts{}

	opts.ProcessTypes = make(map[string]string)
	pt := d.Get("process_types").([]interface{})
	for _, v := range pt {
		for kk, vv := range v.(map[string]interface{}) {
			opts.ProcessTypes[kk] = vv.(string)
		}
	}

	if v, ok := d.GetOk("buildpack_provided_description"); ok {
		opts.BuildpackProvidedDescription = heroku.String(v.(string))
	}
	if v, ok := d.GetOk("checksum"); ok {
		opts.Checksum = heroku.String(v.(string))
	}
	if v, ok := d.GetOk("commit"); ok {
		opts.Commit = heroku.String(v.(string))
	}
	if v, ok := d.GetOk("commit_description"); ok {
		opts.CommitDescription = heroku.String(v.(string))
	}
	if v, ok := d.GetOk("stack"); ok {
		opts.Stack = heroku.String(v.(string))
	}

	slug, err := client.SlugCreate(context.TODO(), app, opts)
	if err != nil {
		return fmt.Errorf("Error creating slug: %s opts %+v", err, opts)
	}

	// Optionally upload slug before setting ID, so that an upload failure
	// causes a resource creation error, is not saved in state.
	if v, ok := d.GetOk("file_path"); ok {
		filePath := v.(string)
		err := uploadSlug(filePath, slug.Blob.Method, slug.Blob.URL)
		if err != nil {
			return err
		}
		// file_path being set indicates a successful upload.
		d.Set("file_path", filePath)
	}

	d.SetId(slug.ID)
	setState(d, slug)

	log.Printf("[INFO] Created slug ID: %s", d.Id())
	return nil
}

func resourceHerokuSlugRead(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*heroku.Service)

	app := d.Get("app").(string)
	slug, err := client.SlugInfo(context.TODO(), app, d.Id())
	if err != nil {
		return fmt.Errorf("Error retrieving slug: %s", err)
	}

	setState(d, slug)

	return nil
}

// A no-op method as there is no DELETE slug in Heroku Platform API.
func resourceHerokuSlugDelete(d *schema.ResourceData, meta interface{}) error {
	log.Printf("[INFO] There is no DELETE for slug resource so this is a no-op. Slug will be removed from state.")
	return nil
}

func uploadSlug(filePath, httpMethod, httpUrl string) error {
	method := strings.ToUpper(httpMethod)
	log.Printf("[DEBUG] Uploading slug '%s' to %s %s", filePath, method, httpUrl)

	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("Error opening slug file_path: %s", err)
	}
	stat, err := file.Stat()
	if err != nil {
		return fmt.Errorf("Error stating slug file_path: %s", err)
	}
	defer file.Close()

	httpClient := &http.Client{}
	req, err := http.NewRequest(method, httpUrl, file)
	if err != nil {
		return fmt.Errorf("Error creating slug upload request: %s", err)
	}
	req.ContentLength = stat.Size()
	log.Printf("[DEBUG] Upload slug request: %+v", req)
	res, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("Error uploading slug: %s", err)
	}

	b, err := httputil.DumpResponse(res, true)
	if err == nil {
		// generate debug output if it's available
		log.Printf("[DEBUG] Slug upload response: %s", b)
	}

	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fmt.Errorf("Unsuccessful HTTP response from slug upload: %s", res.Status)
	}
	return nil
}

func setState(d *schema.ResourceData, slug *heroku.Slug) error {
	blob := []map[string]string{{
		"method": slug.Blob.Method,
		"url":    slug.Blob.URL,
	}}
	if err := d.Set("blob", blob); err != nil {
		log.Printf("[WARN] Error setting blob: %s", err)
	}
	d.Set("buildpack_provided_description", slug.BuildpackProvidedDescription)
	d.Set("checksum", slug.Checksum)
	d.Set("commit", slug.Commit)
	d.Set("commit_description", slug.CommitDescription)
	processTypes := []map[string]string{slug.ProcessTypes}
	if err := d.Set("process_types", processTypes); err != nil {
		log.Printf("[WARN] Error setting process_types: %s", err)
	}
	d.Set("size", slug.Size)
	d.Set("stack_id", slug.Stack.ID)
	d.Set("stack_name", slug.Stack.Name)
	return nil
}
