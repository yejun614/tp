package main

import (
	"crypto/sha256"
	"flag"
	"fmt"
	"log"
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
	ProgramVersion = "v0.1 beta"
	ServiceName    = "tp-secret"
)

var (
	DB *data.Data[ProgramData]
)

type Cmd struct {
	Name        string
	Description string
	Args        []string
	Secret      string
}

type ProgramData struct {
	SecretKey string
	Editors   []string
	Cmds      []Cmd
}

func hash(input string) string {
	h := sha256.New()
	h.Write([]byte(input))
	return string(fmt.Sprintf("%x", h.Sum(nil)))
}

func (cmd *Cmd) SetSecret(data string) error {
	return keyring.Set(ServiceName, hash(cmd.Name), data)
}

func (cmd *Cmd) GetSecret() (string, error) {
	return keyring.Get(ServiceName, hash(cmd.Name))
}

func (cmd *Cmd) RemoveSecret() error {
	return keyring.Delete(ServiceName, hash(cmd.Name))
}

func FindCmds(query string) []Cmd {
	check := []int{}
	result := []Cmd{}

	for index, cmd := range DB.Data.Cmds {
		if cmd.Name == query {
			result = append(result, cmd)
			check = append(check, index)
		}
	}

	for index, cmd := range DB.Data.Cmds {
		if !slices.Contains(check, index) && strings.Contains(cmd.Name, query) {
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
	flagVer := flag.Bool("v", false, "Show program version")
	flagSettings := flag.Bool("s", false, "Edit program settings")
	flag.Parse()
	args := flag.Args()

	usr, err := user.Current()
	if err != nil {
		log.Fatal(err)
	}

	DB = data.New(filepath.Join(usr.HomeDir, ".tp.conf"), ProgramData{
		SecretKey: "",
		Editors:   []string{"micro", "vim", "nano"},
		Cmds: []Cmd{
			{
				Name:        "ping",
				Description: "Send ping localhost",
				Args:        []string{"ping", "localhost"},
			},
		},
	})

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

		names := []string{}
		for index, cmd := range programData.Cmds {
			if slices.Contains(names, cmd.Name) {
				fmt.Println("Duplicated Names (Settings Ignored)")
				fmt.Printf("- %s\n", cmd.Name)
				return
			} else {
				names = append(names, cmd.Name)
			}

			if cmd.Secret != "" {
				cmd.SetSecret(cmd.Secret)
				programData.Cmds[index].Secret = ""
			}
		}

		for _, cmd := range DB.Data.Cmds {
			if !slices.Contains(names, cmd.Name) {
				cmd.RemoveSecret()
			}
		}

		DB.Data = programData
		if err := DB.Save(); err != nil {
			log.Fatal(err)
		}

	} else if DB.Data.SecretKey != "" {
		cmds := FindCmds(DB.Data.SecretKey)
		if len(cmds) != 0 {
			secret, err := cmds[0].GetSecret()
			if err != nil {
				log.Fatal(err)
			} else if secret != "" && secret != "<nil>" {
				fmt.Println(secret)
			}
		}
		DB.Data.SecretKey = ""
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
			fmt.Printf("[%s] %s\n", result.Name, result.Description)
			fmt.Printf("$ %s\n\n", strings.Join(result.Args, " "))
		}

		if len(results) == 0 {
			fmt.Println("Command Not Found")
			return
		} else if len(results) > 1 {
			fmt.Println("Multiple commands found")
			return
		}
		result := results[0]

		if len(args) > 1 {
			result.Args = append(result.Args, args[1:]...)
		}

		var cmd *exec.Cmd
		if len(result.Args) == 1 {
			cmd = exec.Command(result.Args[0])
		} else {
			cmd = exec.Command(result.Args[0], result.Args[1:]...)
		}

		if result.Args[0][:3] == "ssh" {
			cmd.Stdin = os.Stdin

			_, err := result.GetSecret()
			if err == nil {
				ex, err := os.Executable()
				if err == nil {
					DB.Data.SecretKey = result.Name
					if err := DB.Save(); err != nil {
						log.Fatal(err)
					}

					cmd.Env = []string{}
					cmd.Env = append(cmd.Env, fmt.Sprintf("SSH_ASKPASS=%s", ex))
					cmd.Env = append(cmd.Env, "SSH_ASKPASS_REQUIRE=force")
				}
			}

		} else {
			secret, err := result.GetSecret()
			if err != nil {
				log.Fatal(err)
			}

			if secret != "" && secret != "<nil>" {
				w, err := cmd.StdinPipe()
				if err != nil {
					log.Fatal(err)
				}
				w.Write([]byte("print('hello')\n"))
				w.Close()

			} else {
				cmd.Stdin = os.Stdin
			}
		}

		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Start(); err != nil {
			log.Println(err)
			os.Exit(1)
		}

		cmd.Wait()
	}
}
