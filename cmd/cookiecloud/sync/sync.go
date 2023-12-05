package sync

import (
	"fmt"
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"golang.org/x/exp/slices"

	"github.com/sagan/ptool/cmd/cookiecloud"
	"github.com/sagan/ptool/config"
	"github.com/sagan/ptool/site"
	"github.com/sagan/ptool/util"
)

type site_test_result struct {
	sitename string
	url      string
	flag     int
	msg      string
}

var (
	profile  = ""
	siteFlag = ""
	doAction = false
)

var command = &cobra.Command{
	Use:         "sync",
	Annotations: map[string]string{"cobra-prompt-dynamic-suggestions": "cookiecloud.sync"},
	Short:       "Sync cookies with cookiecloud servers.",
	Long: `Sync cookies with cookiecloud servers.
It will get latest cookies from cookiecloud servers. Then use them to update local sites in config file,
Update site which current cookie is no longer valid with the new one.

It will ask for confirm before updating config file, unless --do flag is set.
Be aware that all existing comments in config file will be LOST when updating config file.`,
	RunE: sync,
}

func init() {
	command.Flags().BoolVarP(&doAction, "do", "", false, "Do update the config file without confirm. Be aware that all existing comments in config file will be LOST")
	command.Flags().StringVarP(&siteFlag, "site", "", "", "Comma-separated site or group names. If not set, All sites in config file will be checked and updated")
	command.Flags().StringVarP(&profile, "profile", "", "", "Comma-separated cookiecloud profile names. If not set, All cookiecloud profiles in config file will be used")
	cookiecloud.Command.AddCommand(command)
}

func sync(cmd *cobra.Command, args []string) error {
	cntError := int64(0)
	cookiecloudProfiles := cookiecloud.ParseProfile(profile)
	if len(cookiecloudProfiles) == 0 {
		return fmt.Errorf("no cookiecloud profile specified or found")
	}
	cookiecloudDatas := []cookiecloud.Ccdata_struct{}
	for _, profile := range cookiecloudProfiles {
		data, err := cookiecloud.GetCookiecloudData(profile.Server, profile.Uuid, profile.Password, profile.Proxy)
		if err != nil {
			log.Errorf("Cookiecloud server %s (uuid %s) connection failed: %v\n", profile.Server, profile.Uuid, err)
			cntError++
		} else {
			log.Infof("Cookiecloud server %s (uuid %s) connection ok: %d site cookies found\n",
				profile.Server, profile.Uuid, len(data.Cookie_data))
			cookiecloudDatas = append(cookiecloudDatas, cookiecloud.Ccdata_struct{
				Domain: util.GetUrlDomain(profile.Server),
				Uuid:   profile.Uuid,
				Sites:  profile.Sites,
				Data:   data,
			})
		}
	}
	if len(cookiecloudDatas) == 0 {
		return fmt.Errorf("no cookiecloud server can be connected")
	}
	var sitenames []string
	if siteFlag == "" {
		sitenames = []string{}
		for _, site := range config.Get().Sites {
			if site.Disabled {
				continue
			}
			sitenames = append(sitenames, site.GetName())
		}
	} else {
		sitenames = config.ParseGroupAndOtherNames(strings.Split(siteFlag, ",")...)
	}

	updatesites := []*config.SiteConfigStruct{}
	// sitename => flag.
	// flag: 0 - 初始；1 - 当前配置cookie有效；2 - 站点不存在或当前无法访问；3-当前配置cookie无效；4-已更新cookie;
	var siteFlags = make(map[string]int)
	var siteUrls = make(map[string]string)
	ch := make(chan *site_test_result, len(sitenames))
	for _, sitename := range sitenames {
		go func(sitename string, ch chan<- *site_test_result) {
			siteconfig := config.GetSiteConfig(sitename)
			if siteconfig == nil {
				ch <- &site_test_result{
					sitename: sitename,
					flag:     2,
					msg:      "site not found in config",
				}
				return
			}
			siteInstance, err := site.CreateSiteInternal(sitename, siteconfig, config.Get())
			if err != nil {
				ch <- &site_test_result{
					sitename: sitename,
					flag:     3,
					msg:      fmt.Sprintf("site current cookie is invalid (create instance err: %v)", err),
				}
				return
			}
			log.Tracef("Checking site %s", sitename)
			sitestatus, err := siteInstance.GetStatus()
			if err != nil {
				if strings.Contains(err.Error(), "<network error>") {
					ch <- &site_test_result{
						sitename: sitename,
						url:      siteInstance.GetSiteConfig().Url,
						flag:     2,
						msg:      fmt.Sprintf("site is inaccessible currently (get status error: %v)", err),
					}
				} else {
					ch <- &site_test_result{
						sitename: sitename,
						url:      siteInstance.GetSiteConfig().Url,
						flag:     3,
						msg:      fmt.Sprintf("site current cookie is invalid (get status error: %v)", err),
					}
				}
			} else {
				ch <- &site_test_result{
					sitename: sitename,
					url:      siteInstance.GetSiteConfig().Url,
					flag:     1,
					msg:      fmt.Sprintf("site current cookie is valid (username: %s)", sitestatus.UserName),
				}
			}
		}(sitename, ch)
	}
	for i := 0; i < len(sitenames); i++ {
		result := <-ch
		symbol := ""
		switch result.flag {
		case 1:
			symbol = "✓"
		case 2:
			symbol = "!"
		case 3:
			symbol = "✕"
		}
		siteFlags[result.sitename] = result.flag
		siteUrls[result.sitename] = result.url
		log.Infof("%s site %s: %s // remaining sites: %d", symbol, result.sitename, result.msg, len(sitenames)-i-1)
	}
	for _, sitename := range sitenames {
		if siteFlags[sitename] != 3 {
			continue
		}
		siteconfig := config.GetSiteConfig(sitename)
		for _, cookiecloudData := range cookiecloudDatas {
			if cookiecloudData.Sites != nil &&
				slices.Index(config.ParseGroupAndOtherNames(cookiecloudData.Sites...), sitename) == -1 {
				continue
			}
			newcookie, err := cookiecloudData.Data.GetEffectiveCookie(siteUrls[sitename])
			if newcookie == "" {
				log.Debugf("No cookie found for % site from cookiecloud %s - %s (url=%s, error: %v)",
					sitename, cookiecloudData.Domain, cookiecloudData.Uuid, siteUrls[sitename], err)
				continue
			}
			log.Debugf("Found cookie for %s sitename from cookiecloud %s - %s",
				sitename, cookiecloudData.Domain, cookiecloudData.Uuid,
			)
			newsiteconfig := &config.SiteConfigStruct{}
			util.Assign(newsiteconfig, siteconfig, nil)
			newsiteconfig.Cookie = newcookie
			siteInstance, err := site.CreateSiteInternal(sitename, newsiteconfig, config.Get())
			if err != nil {
				log.Debugf("Site %s new cookie from cookiecloud %s - %s is invalid (create instance error: %v",
					sitename, cookiecloudData.Domain, cookiecloudData.Uuid, err)
				continue
			}
			sitestatus, err := siteInstance.GetStatus()
			if err != nil {
				log.Debugf("Site %s new cookie from cookiecloud %s - %s is invalid (get status error: %v",
					sitename, cookiecloudData.Domain, cookiecloudData.Uuid, err)
				continue
			}
			log.Infof("✓✓site %s new cookie from cookiecloud %s - %s is OK (username: %s)",
				sitename, cookiecloudData.Domain, cookiecloudData.Uuid, sitestatus.UserName)
			siteFlags[sitename] = 4
			updatesites = append(updatesites, newsiteconfig)
		}
	}
	sitesValid := []string{}
	sitesInaccessible := []string{}
	sitesInvalid := []string{}
	sitesUpdated := []string{}
	for sitename, siteflag := range siteFlags {
		switch siteflag {
		case 1:
			sitesValid = append(sitesValid, sitename)
		case 2:
			sitesInaccessible = append(sitesInaccessible, sitename)
		case 3:
			sitesInvalid = append(sitesInvalid, sitename)
		case 4:
			sitesUpdated = append(sitesUpdated, sitename)
		}
	}

	fmt.Printf("Summary (all %d sites):\n", len(sitenames))
	fmt.Printf("✓Sites current-cookie-valid (%d): %s\n", len(sitesValid), strings.Join(sitesValid, ", "))
	fmt.Printf("!Sites inaccessible-now (%d): %s\n", len(sitesInaccessible), strings.Join(sitesInaccessible, ", "))
	fmt.Printf("✕Sites invalid-cookie (no new valid cookie found) (%d): %s\n", len(sitesInvalid), strings.Join(sitesInvalid, ", "))
	fmt.Printf("✓✓Sites success-with-new-cookie (%d): %s\n", len(sitesUpdated), strings.Join(sitesUpdated, ", "))

	if len(updatesites) > 0 {
		configFile := fmt.Sprintf("%s/%s", config.ConfigDir, config.ConfigFile)
		fmt.Printf("\n")
		if !doAction {
			fmt.Printf("Will update the config file (%s). Please be aware that all existing comments will be LOST., are you sure? (yes/no): ", configFile)
			input := ""
			fmt.Scanf("%s", &input)
			if input != "yes" {
				return fmt.Errorf("abort")
			}
		}
		config.UpdateSites(updatesites)
		err := config.Set()
		if err == nil {
			fmt.Printf("Successfully update config file %s", configFile)
		} else {
			log.Fatalf("Failed to update config file %s : %v", configFile, err)
		}
	} else {
		fmt.Printf("!No new cookie found for any site")
	}

	if cntError > 0 {
		return fmt.Errorf("%d errors", cntError)
	}
	return nil
}
