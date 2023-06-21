package add

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	goTorrentParser "github.com/j-muller/go-torrent-parser"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/sagan/ptool/client"
	"github.com/sagan/ptool/cmd"
	"github.com/sagan/ptool/site/tpl"
	"github.com/sagan/ptool/utils"
)

var command = &cobra.Command{
	Use:   "addlocal <client> <filename.torrent>...",
	Short: "Add local torrents to client.",
	Long: `Add local torrents to client.
It's possible to use "*" wildcard in filename to match multiple torrents. eg. "*.torrent".
`,
	Args: cobra.MatchAll(cobra.MinimumNArgs(2), cobra.OnlyValidArgs),
	Run:  add,
}

var (
	paused          = false
	skipCheck       = false
	renameAdded     = false
	deleteAdded     = false
	addCategoryAuto = false
	defaultSite     = ""
	rename          = ""
	addCategory     = ""
	addTags         = ""
	savePath        = ""
)

func init() {
	command.Flags().BoolVarP(&skipCheck, "no-hash", "", false, "Skip hash checking when adding torrents")
	command.Flags().BoolVarP(&renameAdded, "rename-added", "", false, "Rename successfully added torrents to .added extension")
	command.Flags().BoolVarP(&deleteAdded, "delete-added", "", false, "Delete successfully added torrents")
	command.Flags().BoolVarP(&paused, "add-paused", "", false, "Add torrents to client in paused state")
	command.Flags().BoolVarP(&addCategoryAuto, "add-category-auto", "", false, "Automatically set category of added torrent to corresponding sitename")
	command.Flags().StringVarP(&savePath, "add-save-path", "", "", "Set save path of added torrents")
	command.Flags().StringVarP(&defaultSite, "site", "", "", "Set default site of torrents")
	command.Flags().StringVarP(&addCategory, "add-category", "", "", "Manually set category of added torrents")
	command.Flags().StringVarP(&rename, "rename", "", "", "Rename added torrent (for dev/test only)")
	command.Flags().StringVarP(&addTags, "add-tags", "", "", "Set tags of added torrent (comma-separated)")
	cmd.RootCmd.AddCommand(command)
}

func add(cmd *cobra.Command, args []string) {
	clientName := args[0]
	args = args[1:]
	if renameAdded && deleteAdded {
		log.Fatalf("--rename-added and --delete-added flags are NOT compatible")
	}
	clientInstance, err := client.CreateClient(clientName)
	if err != nil {
		log.Fatal(err)
	}
	errCnt := int64(0)
	torrentFiles := utils.ParseFilenameArgs(args...)
	option := &client.TorrentOption{
		Pause:        paused,
		SavePath:     savePath,
		SkipChecking: skipCheck,
		Name:         rename,
	}
	var fixedTags []string
	if addTags != "" {
		fixedTags = strings.Split(addTags, ",")
	}

	cntAll := len(torrentFiles)
	for i, torrentFile := range torrentFiles {
		if strings.HasSuffix(torrentFile, ".added") {
			log.Tracef("!torrent (%d/%d) %s: skipped", i+1, cntAll, torrentFile)
			continue
		}
		torrentContent, err := os.ReadFile(torrentFile)
		if err != nil {
			fmt.Printf("✕torrent (%d/%d) %s: failed to read file (%v)\n", i+1, cntAll, torrentFile, err)
			errCnt++
			continue
		}
		tinfo, err := goTorrentParser.Parse(bytes.NewReader(torrentContent))
		if err != nil {
			fmt.Printf("✕torrent (%d/%d) %s: failed to parse torrent (%v)\n", i+1, cntAll, torrentFile, err)
			errCnt++
			continue
		}
		sitename := ""
		for _, tracker := range tinfo.Announce {
			domain := utils.GetUrlDomain(tracker)
			if domain == "" {
				continue
			}
			sitename = tpl.GuessSiteByDomain(domain, defaultSite)
			if sitename != "" {
				break
			}
		}
		if sitename != "" && addCategoryAuto {
			option.Category = sitename
		} else {
			option.Category = addCategory
		}
		option.Tags = []string{}
		if sitename != "" {
			option.Tags = append(option.Tags, client.GenerateTorrentTagFromSite(sitename))
		}
		option.Tags = append(option.Tags, fixedTags...)
		err = clientInstance.AddTorrent(torrentContent, option, nil)
		if err != nil {
			fmt.Printf("✕torrent (%d/%d) %s: failed to add to client (%v)\n", i+1, cntAll, torrentFile, err)
			errCnt++
			continue
		}
		if renameAdded {
			err := os.Rename(torrentFile, torrentFile+".added")
			if err != nil {
				log.Debugf("Failed to rename successfully added torrent %s to .added extension: %v", torrentFile, err)
			}
		} else if deleteAdded {
			err := os.Remove(torrentFile)
			if err != nil {
				log.Debugf("Failed to delete successfully added torrent %s: %v", torrentFile, err)
			}
		}
		fmt.Printf("✓torrent (%d/%d) %s: added to client\n", i+1, cntAll, torrentFile)
	}
	clientInstance.Close()
	if errCnt > 0 {
		os.Exit(1)
	}
}
