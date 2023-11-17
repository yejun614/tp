package main

import (
	"crypto/sha256"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"

	"golang.org/x/exp/slices"

	"github.com/pelletier/go-toml/v2"
	"github.com/yejun614/go-data"
	"github.com/zalando/go-keyring"
)

const (
	ProgramVersion = "v0.2.0 beta"
	ServiceName    = "tp-secret"
)

var (
	DB *data.Data[ProgramData]
)

type Cmd struct {
	ID          string
	Alias       string
	Description string
	Scripts     []string
	Secret      string
}

type ProgramData struct {
	GetSecrets []string
	Editors    []string
	Shell      string
	Cmds       []Cmd
}

func hash(input string) string {
	h := sha256.New()
	h.Write([]byte(input))
	return string(fmt.Sprintf("%x", h.Sum(nil)))
}

func (cmd *Cmd) SetSecret(data string) error {
	return keyring.Set(ServiceName, hash(cmd.ID), data)
}

func (cmd *Cmd) GetSecret() (string, error) {
	return keyring.Get(ServiceName, hash(cmd.ID))
}

func (cmd *Cmd) RemoveSecret() error {
	return keyring.Delete(ServiceName, hash(cmd.ID))
}

func FindCmds(query string) []Cmd {
	result := []Cmd{}

	for _, cmd := range DB.Data.Cmds {
		if cmd.Alias == query {
			result = append(result, cmd)
			return result
		}
	}

	for _, cmd := range DB.Data.Cmds {
		if strings.Contains(cmd.Alias, query) {
			result = append(result, cmd)
		}
	}

	return result
}

func ScanEditor(intro string) []byte {
	usr, err := user.Current()
	if err != nil {
		log.Fatal(err)
	}
	fpath := filepath.Join(usr.HomeDir, ".temp.tp.txt")

	f, err := os.Create(fpath)
	if err != nil {
		log.Fatal(err)
	} else {
		fmt.Fprintf(f, "%s", intro)
		f.Close()
	}

	editor := ""
	for _, e := range DB.Data.Editors {
		if _, err := exec.LookPath(e); err == nil {
			editor = e
			break
		}
	}
	if editor == "" {
		log.Fatal("cannot open the editor")
	}

	cmd := exec.Command(editor, fpath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatal(err)
	}

	dat, err := os.ReadFile(fpath)
	if err != nil {
		log.Fatal(err)
	}

	if err := os.Remove(fpath); err != nil {
		log.Fatal(err)
	}

	return dat
}

func main() {
	usr, err := user.Current()
	if err != nil {
		log.Fatal(err)
	}

	DB = data.New(filepath.Join(usr.HomeDir, ".tp.conf"), ProgramData{
		GetSecrets: []string{},
		Editors:    []string{"micro", "vim", "nano"},
		Shell:      "bash -c",
		Cmds: []Cmd{
			{
				ID:          "ping-test",
				Alias:       "ping",
				Description: "Send ping localhost",
				Scripts:     []string{"ping localhost"},
			},
		},
	})

	flagVer := flag.Bool("v", false, "Show program version")
	flagSettings := flag.Bool("s", false, "Edit program settings")
	flagSecret := flag.Bool("p", false, "Get secret string")
	flag.Parse()
	args := flag.Args()

	if *flagVer {
		fmt.Println(ProgramVersion)

	} else if *flagSettings {
		b, err := toml.Marshal(DB.Data)
		if err != nil {
			log.Fatal(err)
		}

		programData := ProgramData{}
		err = toml.Unmarshal(ScanEditor(string(b)), &programData)
		if err != nil {
			log.Fatal(err)
		}

		ids := []string{}
		for index, cmd := range programData.Cmds {
			if cmd.ID == "" {
				cmd.ID = hash(fmt.Sprintf("%v", rand.Float64()))
				programData.Cmds[index].ID = cmd.ID
			}

			if slices.Contains(ids, cmd.ID) {
				fmt.Println("Duplicated ID (Settings Ignored)")
				fmt.Printf("- %s\n", cmd.ID)
				return
			} else {
				ids = append(ids, cmd.ID)
			}

			if cmd.Secret != "" {
				cmd.SetSecret(cmd.Secret)
				programData.Cmds[index].Secret = ""
			}
		}

		for _, cmd := range DB.Data.Cmds {
			if !slices.Contains(ids, cmd.ID) {
				fmt.Printf("Remove %s", cmd.Alias)
				if cmd.Description != "" {
					fmt.Printf(" - %s", cmd.Description)
				}
				fmt.Println()

				cmd.RemoveSecret()
			}
		}

		DB.Data = programData
		if err := DB.Save(); err != nil {
			log.Fatal(err)
		}

	} else if *flagSecret {
		id := args[0]
		for _, cmd := range DB.Data.Cmds {
			if cmd.ID[:len(id)] == id {
				secret, err := cmd.GetSecret()
				if err != nil {
					log.Fatal(err)
				}
				fmt.Println(secret)
				return
			}
		}
		panic("Cannot found the secret")

	} else if len(DB.Data.GetSecrets) > 0 {
		target := DB.Data.GetSecrets[0]
		DB.Data.GetSecrets = DB.Data.GetSecrets[1:]

		for _, cmd := range DB.Data.Cmds {
			if cmd.ID == target {
				secret, err := cmd.GetSecret()
				if err != nil {
					log.Fatal(err)
				} else if secret != "" && secret != "<nil>" {
					fmt.Println(secret)
				}
			}
		}

		if err := DB.Save(); err != nil {
			log.Fatal(err)
		}

	} else {
		if len(args) == 0 {
			fmt.Println("No Arguments")
			return
		}

		results := FindCmds(args[0])
		for _, result := range results {
			fmt.Printf("[%s] %s\n", result.Alias, result.Description)
			for _, script := range result.Scripts {
				fmt.Printf("$ %s\n", script)
			}
			fmt.Println()
		}

		if len(results) == 0 {
			fmt.Println("Command Not Found")
			return
		} else if len(results) > 1 {
			fmt.Println("Multiple commands found")
			return
		}
		result := results[0]

		if len(result.Scripts) == 0 {
			fmt.Println("No Scripts")
			return
		}

		cmdArgs := result.Scripts
		cmdArgs = append(cmdArgs, args[1:]...)
		cmdStr := ""
		for index, script := range cmdArgs {
			if script[:3] == "ssh" || script[:3] == "scp" {
				DB.Data.GetSecrets = append(DB.Data.GetSecrets, result.ID)
				script = fmt.Sprintf("%s -oStrictHostKeyChecking=accept-new %s", script[:3], script[4:])
				ex, err := os.Executable()
				if err == nil {
					script = fmt.Sprintf("SSH_ASKPASS=%s SSH_ASKPASS_REQUIRE=force %s", ex, script)
				}
			}
			if index != 0 {
				cmdStr += "&&"
			}
			cmdStr += script
		}

		if err := DB.Save(); err != nil {
			log.Fatal(err)
		}

		sh := strings.Split(DB.Data.Shell, " ")
		sh = append(sh, cmdStr)
		cmd := exec.Command(sh[0], sh[1:]...)

		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Run(); err != nil {
			log.Println(err)
			os.Exit(1)
		}
	}
}
