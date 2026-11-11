package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"mygopia/internal/config"
	"mygopia/internal/kopia"
)

type connectFormState struct {
	modeSeg              int // 0 Connect Existing, 1 Create New
	repoName             string
	provider             string
	password             string
	passwordRepeat       string
	description          string
	readOnly             bool
	showAdvanced         bool
	connecting           bool
	errMsg               string
	pendingCreatedRepoID string

	// Local filesystem
	fsPath string

	// S3 / S3-compatible
	s3Bucket         string
	s3Endpoint       string
	s3Region         string
	s3AccessKey      string
	s3SecretKey      string
	s3SessionToken   string
	s3Prefix         string
	s3DoNotUseTLS    bool
	s3DoNotVerifyTLS bool

	// B2
	b2Bucket string
	b2KeyID  string
	b2Key    string
	b2Prefix string

	// SFTP
	sftpHost        string
	sftpPort        string
	sftpUsername    string
	sftpPassword    string
	sftpKeyfile     string
	sftpKnownHosts  string
	sftpPath        string
	sftpExternalSSH bool
	sftpSSHCommand  string
	sftpSSHArgs     string

	// WebDAV
	webdavURL  string
	webdavUser string
	webdavPass string

	// Rclone
	rcloneRemotePath string
	rcloneExe        string
	rcloneArgs       string

	// GCS
	gcsBucket          string
	gcsPrefix          string
	gcsCredentialsFile string

	// Azure
	azContainer string
	azAccount   string
	azKey       string
	azSASToken  string
	azDomain    string
	azPrefix    string

	// Kopia Server & Token
	serverURL         string
	serverFingerprint string
	token             string

	// Create-mode format options
	encryption string
	hash       string
	splitter   string
	ecc        string
	eccPercent float64
}

var providerKeys = []string{
	"filesystem",
	"s3",
	"b2",
	"sftp",
	"webdav",
	"rclone",
	"gcs",
	"azureBlob",
	"kopia-server",
	"token",
}

func providerLabel(key string) string {
	switch key {
	case "filesystem":
		return tr("Local Folder / NAS")
	case "s3":
		return tr("S3 / S3-Compatible")
	case "b2":
		return tr("Backblaze B2")
	case "sftp":
		return tr("SFTP / SSH")
	case "webdav":
		return tr("WebDAV")
	case "rclone":
		return tr("Rclone Remote")
	case "gcs":
		return tr("Google Cloud Storage")
	case "azureBlob":
		return tr("Azure Blob Storage")
	case "kopia-server":
		return tr("Kopia Server")
	case "token":
		return tr("Repository Token")
	default:
		return key
	}
}

func (a *App) initConnectForm() {
	var defKnownHosts string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		kh := filepath.Join(home, ".ssh", "known_hosts")
		if st, err := os.Stat(kh); err == nil && !st.IsDir() {
			defKnownHosts = kh
		}
	}
	a.connectForm = connectFormState{
		provider:       "filesystem",
		sftpPort:       "22",
		sftpKnownHosts: defKnownHosts,
		sftpSSHCommand: "ssh",
		rcloneExe:      strings.TrimSpace(a.settings.RcloneBinary),
	}
}

func (a *App) openConnectWizard(asNewRepoProfile bool) {
	a.initConnectForm()
	a.connectAsNewRepo = asNewRepoProfile
	if asNewRepoProfile {
		a.connectForm.repoName = fmt.Sprintf("Vault %d", len(a.settings.Repositories)+1)
	} else {
		prof, _ := a.activeRepoState()
		a.connectForm.repoName = prof.Name
	}
	a.connectModalOpen = true
}

func (a *App) renderRepoOnboarding(c *ui.Context, prof config.RepoProfile, st kopia.RepoSnapshotState) {
	p := paletteOf(c)
	ui.Scroll(c.Key("onboarding-scroll")).Fill().Padding(28).Children(func() {
		ui.Column(c).MaxWidth(640).Margin(0, 0).Gap(16).Children(func() {
			if st.LastError != "" {
				ui.Row(c).Padding(10, 14).Radius(8).Background(p.dangerSoft).Border(1, c.Theme().Danger).Children(func() {
					ui.Text(c, st.LastError).TextColor(c.Theme().Danger).FontSize(12.5)
				})
			}
			card(c, func() {
				ui.Row(c).AlignItems(ui.Center).Gap(10).Children(func() {
					ui.Icon(c, iconRepo).FontSize(20).TextColor(p.accent)
					ui.Column(c).Grow(1).Gap(2).Children(func() {
						ui.Text(c, tr("Connect or Create")).FontSize(16).FontWeight(600)
						ui.Text(c, prof.ConfigFile).FontSize(11.5).TextColor(p.muted).SingleLine()
					})
				})
				ui.Box(c).Height(1).Background(p.border)
				a.connectAsNewRepo = false
				a.renderConnectFormFields(c, st.Algorithms)
				ui.Box(c).Height(1).Background(p.border)
				a.renderConnectFormActions(c, false)
			})
		})
	})
}

func (a *App) renderConnectModal(c *ui.Context) {
	if !a.connectModalOpen {
		if a.connectForm.pendingCreatedRepoID != "" {
			a.removeRepoProfileFromList(a.connectForm.pendingCreatedRepoID)
			a.connectForm.pendingCreatedRepoID = ""
		}
		return
	}
	p := paletteOf(c)
	_, st := a.activeRepoState()
	ui.Modal(c, &a.connectModalOpen, func() {
		ui.Column(c).Width(560).MaxHeight(640).Gap(14).Children(func() {
			ui.Text(c, tr("Connect or Create")).FontSize(16).FontWeight(600)
			ui.Scroll(c.Key("connect-modal-scroll")).Grow(1).Padding(2, 6, 4, 2).Children(func() {
				a.renderConnectFormFields(c, st.Algorithms)
			})
			ui.Box(c).Height(1).Background(p.border).Shrink(0)
			a.renderConnectFormActions(c, true)
		})
	})
}

func (a *App) renderConnectFormFields(c *ui.Context, algos *kopia.SupportedAlgorithmsResponse) {
	p := paletteOf(c)
	f := &a.connectForm

	ui.Column(c).Gap(16).Children(func() {
		ui.Row(c).Children(func() {
			ui.Segmented(c, &f.modeSeg, tr("Connect Existing"), tr("Create New")).Label(tr("Connect or Create"))
		})

		// Provider selection mapped through translated labels
		labels := make([]string, len(providerKeys))
		curLabel := providerLabel(f.provider)
		for i, k := range providerKeys {
			// Token and Kopia Server only apply to Connect Existing
			labels[i] = providerLabel(k)
		}
		if f.modeSeg == 1 && (f.provider == "token" || f.provider == "kopia-server") {
			f.provider = "filesystem"
			curLabel = providerLabel(f.provider)
		}
		availKeys := providerKeys
		if f.modeSeg == 1 {
			availKeys = providerKeys[:8]
			labels = labels[:8]
		}

		if f.errMsg != "" {
			ui.Row(c).Padding(8, 12).Radius(7).Background(p.dangerSoft).Border(1, c.Theme().Danger).Children(func() {
				ui.Text(c, f.errMsg).TextColor(c.Theme().Danger).FontSize(12)
			})
		}

		ui.Form(c, func() {
			if a.connectAsNewRepo {
				ui.Field(c, tr("Repository Name"), func() {
					ui.TextInput(c.Key("conn-reponame"), &f.repoName).Placeholder("Work Backups")
				})
			}

			ui.Field(c, tr("Storage Provider"), func() {
				if ui.Select(c.Key("conn-provider"), &curLabel, labels).Changed() {
					for i, lb := range labels {
						if lb == curLabel && i < len(availKeys) {
							f.provider = availKeys[i]
							break
						}
					}
				}
			})

			switch f.provider {
			case "filesystem":
				ui.Field(c, tr("Repository Directory"), func() {
					ui.Row(c).Gap(8).Children(func() {
						ui.TextInput(c.Key("fs-path"), &f.fsPath).Placeholder("/Volumes/BackupDrive/kopia-repo").Grow(1)
						if a.desktopRunning {
							if ui.Button(c, tr("Browse…")).Clicked() {
								go a.pickDirectoryInto(&f.fsPath)
							}
						}
					})
				})

			case "s3":
				ui.Field(c, tr("Bucket Name"), func() {
					ui.TextInput(c.Key("s3-bucket"), &f.s3Bucket).Placeholder("my-kopia-backups")
				})
				ui.Field(c, tr("Endpoint"), func() {
					ui.TextInput(c.Key("s3-endpoint"), &f.s3Endpoint).Placeholder("s3.amazonaws.com / <account>.r2.cloudflarestorage.com")
				})
				ui.Field(c, tr("Region"), func() {
					ui.TextInput(c.Key("s3-region"), &f.s3Region).Placeholder("us-east-1 / auto")
				})
				ui.Field(c, tr("Access Key ID"), func() {
					ui.TextInput(c.Key("s3-ak"), &f.s3AccessKey)
				})
				ui.Field(c, tr("Secret Access Key"), func() {
					ui.TextInput(c.Key("s3-sk"), &f.s3SecretKey).Password()
				})
				ui.Field(c, tr("Session Token"), func() {
					ui.TextInput(c.Key("s3-stok"), &f.s3SessionToken).Password().Placeholder(tr("Optional"))
				})
				ui.Field(c, tr("Object Prefix"), func() {
					ui.TextInput(c.Key("s3-prefix"), &f.s3Prefix).Placeholder(tr("Optional"))
				})
				ui.Field(c, "", func() {
					ui.Column(c).Gap(6).Children(func() {
						ui.Checkbox(c, &f.s3DoNotUseTLS, tr("Use HTTP (Disable TLS)"))
						ui.Checkbox(c, &f.s3DoNotVerifyTLS, tr("Skip TLS Certificate Verification"))
					})
				})

			case "b2":
				ui.Field(c, tr("Bucket Name"), func() {
					ui.TextInput(c.Key("b2-bucket"), &f.b2Bucket)
				})
				ui.Field(c, tr("Key ID"), func() {
					ui.TextInput(c.Key("b2-keyid"), &f.b2KeyID)
				})
				ui.Field(c, tr("Application Key"), func() {
					ui.TextInput(c.Key("b2-key"), &f.b2Key).Password()
				})
				ui.Field(c, tr("Object Prefix"), func() {
					ui.TextInput(c.Key("b2-prefix"), &f.b2Prefix).Placeholder(tr("Optional"))
				})

			case "sftp":
				ui.Field(c, tr("Host"), func() {
					ui.Row(c).Gap(8).Children(func() {
						ui.TextInput(c.Key("sftp-host"), &f.sftpHost).Placeholder("nas.local").Grow(1)
						ui.TextInput(c.Key("sftp-port"), &f.sftpPort).Placeholder("22").Width(80)
					})
				})
				ui.Field(c, tr("Username"), func() {
					ui.TextInput(c.Key("sftp-user"), &f.sftpUsername)
				})
				ui.Field(c, tr("Remote Path"), func() {
					ui.TextInput(c.Key("sftp-path"), &f.sftpPath).Placeholder("/mnt/backups/kopia")
				})
				ui.Field(c, "", func() {
					ui.Checkbox(c, &f.sftpExternalSSH, tr("Use External SSH Command (Agent / ~/.ssh/config)"))
				})
				if f.sftpExternalSSH {
					ui.Field(c, tr("SSH Command"), func() {
						ui.TextInput(c.Key("sftp-ext-cmd"), &f.sftpSSHCommand).Placeholder("ssh")
					})
					ui.Field(c, tr("SSH Arguments"), func() {
						ui.TextInput(c.Key("sftp-ext-args"), &f.sftpSSHArgs).Placeholder(tr("Optional"))
					})
				} else {
					ui.Field(c, tr("Password"), func() {
						ui.TextInput(c.Key("sftp-pass"), &f.sftpPassword).Password().Placeholder(tr("Optional"))
					})
					ui.Field(c, tr("SSH Key File"), func() {
						ui.Row(c).Gap(8).Children(func() {
							ui.TextInput(c.Key("sftp-key"), &f.sftpKeyfile).Placeholder("~/.ssh/id_ed25519").Grow(1)
							if a.desktopRunning {
								if ui.Button(c, tr("Browse…")).Clicked() {
									go a.pickFileInto(&f.sftpKeyfile)
								}
							}
						})
					})
					ui.Field(c, tr("Known Hosts File"), func() {
						ui.Row(c).Gap(8).Children(func() {
							ui.TextInput(c.Key("sftp-kh"), &f.sftpKnownHosts).Placeholder("~/.ssh/known_hosts").Grow(1)
							if a.desktopRunning {
								if ui.Button(c, tr("Browse…")).Clicked() {
									go a.pickFileInto(&f.sftpKnownHosts)
								}
							}
						})
					})
				}

			case "webdav":
				ui.Field(c, tr("Server URL"), func() {
					ui.TextInput(c.Key("dav-url"), &f.webdavURL).Placeholder("https://dav.example.com/kopia")
				})
				ui.Field(c, tr("Username"), func() {
					ui.TextInput(c.Key("dav-user"), &f.webdavUser)
				})
				ui.Field(c, tr("Password"), func() {
					ui.TextInput(c.Key("dav-pass"), &f.webdavPass).Password()
				})

			case "rclone":
				ui.Field(c, tr("Rclone Remote Path (remote:path)"), func() {
					ui.TextInput(c.Key("rclone-path"), &f.rcloneRemotePath).Placeholder("gdrive:kopia-backup")
				})
				detRclone := a.detectedRclone
				if detRclone == "" {
					detRclone = tr("Not detected")
				}
				rclonePlaceholder := config.ExampleRcloneBinaryPath()
				if a.detectedRclone != "" {
					rclonePlaceholder = a.detectedRclone
				}
				ui.Field(c, tr("Rclone Binary Path"), func() {
					ui.Row(c).Gap(8).Children(func() {
						ui.TextInput(c.Key("rclone-exe"), &f.rcloneExe).Placeholder(rclonePlaceholder).Grow(1)
						if a.desktopRunning {
							if ui.Button(c, tr("Browse…")).Clicked() {
								go a.pickFileInto(&f.rcloneExe)
							}
						}
					})
				}).Description(trf("Leave blank to auto-detect (currently: %s)", detRclone))
				ui.Field(c, tr("Rclone Arguments"), func() {
					ui.TextInput(c.Key("rclone-args"), &f.rcloneArgs).Placeholder("--config ~/.config/rclone/rclone.conf")
				})

			case "gcs":
				ui.Field(c, tr("Bucket Name"), func() {
					ui.TextInput(c.Key("gcs-bucket"), &f.gcsBucket)
				})
				ui.Field(c, tr("Object Prefix"), func() {
					ui.TextInput(c.Key("gcs-prefix"), &f.gcsPrefix).Placeholder(tr("Optional"))
				})
				ui.Field(c, tr("Service Account JSON File"), func() {
					ui.Row(c).Gap(8).Children(func() {
						ui.TextInput(c.Key("gcs-sa"), &f.gcsCredentialsFile).Placeholder(tr("Optional")).Grow(1)
						if a.desktopRunning {
							if ui.Button(c, tr("Browse…")).Clicked() {
								go a.pickFileInto(&f.gcsCredentialsFile)
							}
						}
					})
				})

			case "azureBlob":
				ui.Field(c, tr("Container Name"), func() {
					ui.TextInput(c.Key("az-container"), &f.azContainer)
				})
				ui.Field(c, tr("Storage Account"), func() {
					ui.TextInput(c.Key("az-acct"), &f.azAccount)
				})
				ui.Field(c, tr("Storage Access Key"), func() {
					ui.TextInput(c.Key("az-key"), &f.azKey).Password().Placeholder(tr("Optional"))
				})
				ui.Field(c, tr("SAS Token"), func() {
					ui.TextInput(c.Key("az-sas"), &f.azSASToken).Password().Placeholder(tr("Optional"))
				})
				ui.Field(c, tr("Storage Domain"), func() {
					ui.TextInput(c.Key("az-domain"), &f.azDomain).Placeholder("blob.core.windows.net")
				})
				ui.Field(c, tr("Object Prefix"), func() {
					ui.TextInput(c.Key("az-prefix"), &f.azPrefix).Placeholder(tr("Optional"))
				})

			case "kopia-server":
				ui.Field(c, tr("Server URL"), func() {
					ui.TextInput(c.Key("ks-url"), &f.serverURL).Placeholder("https://kopia.example.com:51515")
				})
				ui.Field(c, tr("TLS Cert Fingerprint (SHA256)"), func() {
					ui.TextInput(c.Key("ks-fp"), &f.serverFingerprint).Placeholder("64-hex SHA256")
				})

			case "token":
				ui.Field(c, tr("Paste Kopia Token"), func() {
					ui.TextInput(c.Key("ks-token"), &f.token).Password().Placeholder("eyJ...")
				})
			}

			if f.provider != "token" {
				ui.Field(c, tr("Repository Password"), func() {
					ui.TextInput(c.Key("repo-pass"), &f.password).Password()
				})
				if f.modeSeg == 1 {
					ui.Field(c, tr("Confirm Password"), func() {
						ui.TextInput(c.Key("repo-pass2"), &f.passwordRepeat).Password()
					})
				}
			}

			ui.Field(c, tr("Client Description"), func() {
				ui.TextInput(c.Key("repo-desc"), &f.description).Placeholder("My Mac")
			})

			if f.modeSeg == 0 {
				ui.Field(c, "", func() {
					ui.Checkbox(c, &f.readOnly, tr("Read-only Connection"))
				})
			}
		})

		// Advanced repository format options in Create mode
		if f.modeSeg == 1 {
			ui.Column(c).Gap(10).Children(func() {
				ui.Checkbox(c, &f.showAdvanced, tr("Advanced Format Options"))
				if f.showAdvanced {
					encChoices := algorithmNames(algosField(algos, "enc"), "AES256-GCM-HMAC-SHA256", "CHACHA20-POLY1305-HMAC-SHA256")
					hashChoices := algorithmNames(algosField(algos, "hash"), "BLAKE2B-256-128", "BLAKE3-256-128", "HMAC-SHA256-128")
					splitChoices := algorithmNames(algosField(algos, "split"), "DYNAMIC-4M-BUZHASH", "FIXED-4M")
					eccDisabledLabel := tr("Disabled (Default)")
					eccChoices := append([]string{eccDisabledLabel}, algorithmNames(algosField(algos, "ecc"), "REED-SOLOMON-CRC32")...)

					if f.encryption == "" && len(encChoices) > 0 {
						if algos != nil && algos.DefaultEncryption != "" {
							f.encryption = algos.DefaultEncryption
						} else {
							f.encryption = encChoices[0]
						}
					}
					if f.hash == "" && len(hashChoices) > 0 {
						if algos != nil && algos.DefaultHash != "" {
							f.hash = algos.DefaultHash
						} else {
							f.hash = hashChoices[0]
						}
					}
					if f.splitter == "" && len(splitChoices) > 0 {
						if algos != nil && algos.DefaultSplitter != "" {
							f.splitter = algos.DefaultSplitter
						} else {
							f.splitter = splitChoices[0]
						}
					}

					curECC := f.ecc
					if curECC == "" {
						curECC = eccDisabledLabel
					}

					ui.Column(c).Padding(14).Radius(8).Background(p.subtleBg).Border(1, p.borderSoft).Children(func() {
						ui.Form(c, func() {
							ui.Field(c, tr("Encryption Algorithm"), func() {
								ui.Select(c.Key("adv-enc"), &f.encryption, encChoices)
							})
							ui.Field(c, tr("Hash Algorithm"), func() {
								ui.Select(c.Key("adv-hash"), &f.hash, hashChoices)
							})
							ui.Field(c, tr("Content Splitter"), func() {
								ui.Select(c.Key("adv-split"), &f.splitter, splitChoices)
							})
							ui.Field(c, tr("Reed-Solomon ECC"), func() {
								if ui.Select(c.Key("adv-ecc"), &curECC, eccChoices).Changed() {
									if curECC == eccDisabledLabel {
										f.ecc = ""
									} else {
										f.ecc = curECC
									}
								}
							})
							if f.ecc != "" {
								ui.Field(c, tr("ECC Overhead (%)"), func() {
									ui.NumberInput(c.Key("adv-ecc-pct"), &f.eccPercent, 0, 20, 1).Width(140)
								})
							}
						})
					})
				}
			})
		}
	})
}

func (f *connectFormState) clearSecrets() {
	f.password = ""
	f.passwordRepeat = ""
	f.token = ""
	f.s3SecretKey = ""
	f.s3SessionToken = ""
	f.b2Key = ""
	f.sftpPassword = ""
	f.webdavPass = ""
	f.azKey = ""
	f.azSASToken = ""
}

func (a *App) renderConnectFormActions(c *ui.Context, insideModal bool) {
	f := &a.connectForm
	ui.Row(c).Gap(8).Justify(ui.End).Shrink(0).Children(func() {
		if insideModal {
			if ui.Button(c, tr("Cancel")).Disabled(f.connecting).Clicked() {
				f.clearSecrets()
				a.connectModalOpen = false
			}
		}
		submitLabel := tr("Connect Repository")
		if f.modeSeg == 1 {
			submitLabel = tr("Initialize & Connect")
		}
		ui.PrimaryButton(c, submitLabel).Disabled(f.connecting).OnClick(func() {
			a.submitConnectOrCreate()
		})
	})
}

func algosField(a *kopia.SupportedAlgorithmsResponse, kind string) []kopia.AlgorithmInfo {
	if a == nil {
		return nil
	}
	switch kind {
	case "enc":
		return a.SupportedEncryptionAlgorithms
	case "hash":
		return a.SupportedHashAlgorithms
	case "split":
		return a.SupportedSplitterAlgorithms
	case "ecc":
		return a.SupportedECCAlgorithms
	case "comp":
		return a.SupportedCompressionAlgorithms
	}
	return nil
}

func algorithmNames(list []kopia.AlgorithmInfo, fallbacks ...string) []string {
	if len(list) == 0 {
		return fallbacks
	}
	out := make([]string, 0, len(list))
	for _, it := range list {
		if !it.Deprecated && it.ID != "" {
			out = append(out, it.ID)
		}
	}
	if len(out) == 0 {
		return fallbacks
	}
	return out
}

func (a *App) pickDirectoryInto(dst *string) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent:    a.window,
		Directory: true,
	})
	if err == nil && len(paths) > 0 {
		a.uiUpdate(func() {
			*dst = paths[0]
		})
	}
}

func (a *App) pickFileInto(dst *string) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent: a.window,
	})
	if err == nil && len(paths) > 0 {
		a.uiUpdate(func() {
			*dst = paths[0]
		})
	}
}

// splitCommandArgs splits shell-style CLI arguments (supporting single/double quotes) and expands ~/ paths.
func splitCommandArgs(raw string) []string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil
	}
	var out []string
	var cur strings.Builder
	var quote rune
	inToken := false
	for i := 0; i < len(s); {
		ch, width := utf8.DecodeRuneInString(s[i:])
		i += width
		if quote != 0 {
			if ch == quote {
				quote = 0
			} else if ch == '\\' && quote == '"' && i < len(s) {
				nextCh, nextW := utf8.DecodeRuneInString(s[i:])
				i += nextW
				cur.WriteRune(nextCh)
			} else {
				cur.WriteRune(ch)
			}
			continue
		}
		switch ch {
		case '\'', '"':
			quote = ch
			inToken = true
		case ' ', '\t', '\r', '\n':
			if inToken {
				out = append(out, expandFlagHomePath(cur.String()))
				cur.Reset()
				inToken = false
			}
		default:
			cur.WriteRune(ch)
			inToken = true
		}
	}
	if inToken {
		out = append(out, expandFlagHomePath(cur.String()))
	}
	return out
}

func expandFlagHomePath(tok string) string {
	if k, v, ok := strings.Cut(tok, "="); ok && strings.HasPrefix(k, "-") && strings.HasPrefix(v, "~") {
		return k + "=" + config.ExpandPath(v)
	}
	if strings.HasPrefix(tok, "~") {
		return config.ExpandPath(tok)
	}
	return tok
}

func (a *App) submitConnectOrCreate() {
	f := a.connectForm
	if f.provider != "token" && strings.TrimSpace(f.password) == "" {
		a.connectForm.errMsg = tr("Password is required")
		return
	}
	if f.modeSeg == 1 && f.password != f.passwordRepeat {
		a.connectForm.errMsg = tr("Passwords do not match")
		return
	}

	connReq, err := buildConnectRequest(f, a.detectedRclone)
	if err != nil {
		a.connectForm.errMsg = err.Error()
		return
	}

	a.connectForm.errMsg = ""
	a.connectForm.connecting = true

	targetRepoID := a.settings.ActiveRepoID
	createProfile := a.connectAsNewRepo
	if createProfile {
		if a.connectForm.pendingCreatedRepoID != "" {
			targetRepoID = a.connectForm.pendingCreatedRepoID
			for i := range a.settings.Repositories {
				if a.settings.Repositories[i].ID == targetRepoID && strings.TrimSpace(f.repoName) != "" {
					a.settings.Repositories[i].Name = strings.TrimSpace(f.repoName)
				}
			}
		} else {
			createdProf := config.NewRepoProfile(a.settings.EffectiveConfigDir(), f.repoName)
			a.connectForm.pendingCreatedRepoID = createdProf.ID
			a.settings.Repositories = append(a.settings.Repositories, createdProf)
			a.settings.ActiveRepoID = createdProf.ID
			targetRepoID = createdProf.ID
		}
		_ = config.Save(a.userDataDir, a.settings)
		if a.manager != nil {
			a.manager.SyncProfiles(a.settings.Repositories)
		}
	}

	isCreate := f.modeSeg == 1
	newOpts := kopia.NewRepositoryOptions{
		BlockFormat: kopia.BlockFormatOptions{
			Encryption:         f.encryption,
			Hash:               f.hash,
			ECC:                f.ecc,
			ECCOverheadPercent: int(f.eccPercent),
		},
		ObjectFormat: kopia.ObjectFormatOptions{
			Splitter: f.splitter,
		},
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()

		if a.manager == nil {
			a.uiUpdate(func() {
				a.connectForm.connecting = false
			})
			return
		}
		cli, err := a.manager.EnsureStarted(ctx, targetRepoID)
		if err != nil {
			a.uiUpdate(func() {
				a.connectForm.connecting = false
				a.connectForm.errMsg = err.Error()
			})
			return
		}

		if isCreate {
			err = cli.CreateRepo(ctx, kopia.CreateRepositoryRequest{
				ConnectRepositoryRequest: connReq,
				NewRepositoryOptions:     newOpts,
			})
		} else {
			err = cli.ConnectRepo(ctx, connReq)
		}
		if err != nil {
			a.uiUpdate(func() {
				a.connectForm.connecting = false
				a.connectForm.errMsg = err.Error()
			})
			return
		}

		a.manager.RefreshRepo(ctx, targetRepoID, true)
		a.uiUpdate(func() {
			a.connectForm.connecting = false
			a.connectForm.pendingCreatedRepoID = ""
			a.connectForm.clearSecrets()
			a.connectModalOpen = false
			a.settings.Repositories = config.DiscoverProfiles(a.settings.Repositories, a.settings.EffectiveConfigDir())
			_ = config.Save(a.userDataDir, a.settings)
			if a.manager != nil {
				a.manager.SyncProfiles(a.settings.Repositories)
			}
			if isCreate {
				a.pendingToast = tr("Repository created and connected")
			} else {
				a.pendingToast = tr("Repository connected")
			}
		})
	}()
}

func buildConnectRequest(f connectFormState, detectedRclone string) (kopia.ConnectRepositoryRequest, error) {
	req := kopia.ConnectRepositoryRequest{
		Password: f.password,
		ClientOptions: kopia.ClientOptions{
			Description: strings.TrimSpace(f.description),
			ReadOnly:    f.readOnly && f.modeSeg == 0,
		},
	}

	switch f.provider {
	case "token":
		tok := strings.TrimSpace(f.token)
		if tok == "" {
			return req, fmt.Errorf("%s", tr("Token is required"))
		}
		req.Token = tok
		return req, nil

	case "kopia-server":
		u := strings.TrimSpace(f.serverURL)
		if u == "" {
			return req, fmt.Errorf("%s", tr("Server URL is required"))
		}
		req.APIServer = &kopia.APIServerInfo{
			BaseURL:                             u,
			TrustedServerCertificateFingerprint: strings.TrimSpace(f.serverFingerprint),
		}
		return req, nil

	case "filesystem":
		p := config.ExpandPath(f.fsPath)
		if p == "" {
			return req, fmt.Errorf("%s", tr("Repository directory path is required"))
		}
		req.Storage = kopia.StorageConnection{
			Type:   "filesystem",
			Config: map[string]any{"path": p},
		}

	case "s3":
		if strings.TrimSpace(f.s3Bucket) == "" {
			return req, fmt.Errorf("%s", tr("S3 bucket name is required"))
		}
		endpoint := strings.TrimSpace(f.s3Endpoint)
		if endpoint == "" {
			endpoint = "s3.amazonaws.com"
		}
		cfg := map[string]any{
			"bucket":          strings.TrimSpace(f.s3Bucket),
			"endpoint":        endpoint,
			"region":          strings.TrimSpace(f.s3Region),
			"accessKeyID":     strings.TrimSpace(f.s3AccessKey),
			"secretAccessKey": f.s3SecretKey,
			"prefix":          strings.TrimSpace(f.s3Prefix),
			"doNotUseTLS":     f.s3DoNotUseTLS,
			"doNotVerifyTLS":  f.s3DoNotVerifyTLS,
		}
		if st := strings.TrimSpace(f.s3SessionToken); st != "" {
			cfg["sessionToken"] = st
		}
		req.Storage = kopia.StorageConnection{
			Type:   "s3",
			Config: cfg,
		}

	case "b2":
		if strings.TrimSpace(f.b2Bucket) == "" {
			return req, fmt.Errorf("%s", tr("B2 bucket name is required"))
		}
		req.Storage = kopia.StorageConnection{
			Type: "b2",
			Config: map[string]any{
				"bucket": strings.TrimSpace(f.b2Bucket),
				"keyId":  strings.TrimSpace(f.b2KeyID),
				"key":    f.b2Key,
				"prefix": strings.TrimSpace(f.b2Prefix),
			},
		}

	case "sftp":
		host := strings.TrimSpace(f.sftpHost)
		user := strings.TrimSpace(f.sftpUsername)
		remotePath := strings.TrimSpace(f.sftpPath)
		if host == "" {
			return req, fmt.Errorf("%s", tr("SFTP host is required"))
		}
		if user == "" || remotePath == "" {
			return req, fmt.Errorf("%s", tr("SFTP username and remote path are required"))
		}
		if strings.HasPrefix(host, "-") || strings.ContainsAny(host, " \t\r\n\x00") {
			return req, fmt.Errorf("invalid SFTP host %q", host)
		}
		if strings.HasPrefix(user, "-") || strings.ContainsAny(user, " \t\r\n\x00") {
			return req, fmt.Errorf("invalid SFTP username %q", user)
		}
		port, _ := strconv.Atoi(strings.TrimSpace(f.sftpPort))
		if port <= 0 {
			port = 22
		}
		cfg := map[string]any{
			"host":        host,
			"port":        port,
			"username":    user,
			"path":        remotePath,
			"externalSSH": f.sftpExternalSSH,
		}
		if f.sftpExternalSSH {
			sshCmd := config.ExpandPath(f.sftpSSHCommand)
			if sshCmd == "" {
				sshCmd = "ssh"
			}
			if strings.HasPrefix(sshCmd, "-") || (sshCmd != "ssh" && !filepath.IsAbs(sshCmd)) {
				return req, fmt.Errorf("invalid SSH command path %q", sshCmd)
			}
			cfg["sshCommand"] = sshCmd
			if args := strings.TrimSpace(f.sftpSSHArgs); args != "" {
				cfg["sshArguments"] = args
			}
		} else {
			keyPath := config.ExpandPath(f.sftpKeyfile)
			khPath := config.ExpandPath(f.sftpKnownHosts)
			if f.sftpPassword == "" && keyPath == "" {
				return req, fmt.Errorf("%s", tr("SFTP password or SSH key file is required"))
			}
			if khPath == "" {
				return req, fmt.Errorf("%s", tr("SFTP known_hosts file is required"))
			}
			cfg["password"] = f.sftpPassword
			cfg["keyfile"] = keyPath
			cfg["knownHostsFile"] = khPath
		}
		req.Storage = kopia.StorageConnection{
			Type:   "sftp",
			Config: cfg,
		}

	case "webdav":
		if strings.TrimSpace(f.webdavURL) == "" {
			return req, fmt.Errorf("%s", tr("WebDAV server URL is required"))
		}
		req.Storage = kopia.StorageConnection{
			Type: "webdav",
			Config: map[string]any{
				"url":      strings.TrimSpace(f.webdavURL),
				"username": strings.TrimSpace(f.webdavUser),
				"password": f.webdavPass,
			},
		}

	case "rclone":
		if strings.TrimSpace(f.rcloneRemotePath) == "" {
			return req, fmt.Errorf("%s", tr("Rclone remote path is required"))
		}
		rcloneCfg := map[string]any{
			"remotePath": strings.TrimSpace(f.rcloneRemotePath),
		}
		exePath := config.ExpandPath(f.rcloneExe)
		if exePath != "" {
			if strings.HasPrefix(exePath, "-") || (strings.ContainsAny(exePath, `/\`) && !filepath.IsAbs(exePath)) {
				return req, fmt.Errorf("invalid rclone binary path %q", exePath)
			}
		} else if strings.TrimSpace(detectedRclone) != "" {
			exePath = strings.TrimSpace(detectedRclone)
		}
		if exePath != "" {
			rcloneCfg["rcloneExe"] = exePath
		}
		if args := splitCommandArgs(f.rcloneArgs); len(args) > 0 {
			rcloneCfg["rcloneArgs"] = args
		}
		req.Storage = kopia.StorageConnection{
			Type:   "rclone",
			Config: rcloneCfg,
		}

	case "gcs":
		if strings.TrimSpace(f.gcsBucket) == "" {
			return req, fmt.Errorf("%s", tr("GCS bucket name is required"))
		}
		req.Storage = kopia.StorageConnection{
			Type: "gcs",
			Config: map[string]any{
				"bucket":          strings.TrimSpace(f.gcsBucket),
				"prefix":          strings.TrimSpace(f.gcsPrefix),
				"credentialsFile": config.ExpandPath(f.gcsCredentialsFile),
			},
		}

	case "azureBlob":
		if strings.TrimSpace(f.azContainer) == "" || strings.TrimSpace(f.azAccount) == "" {
			return req, fmt.Errorf("%s", tr("Azure container and storage account are required"))
		}
		if strings.TrimSpace(f.azKey) == "" && strings.TrimSpace(f.azSASToken) == "" {
			return req, fmt.Errorf("%s", tr("Azure storage access key or SAS token is required"))
		}
		azCfg := map[string]any{
			"container":      strings.TrimSpace(f.azContainer),
			"storageAccount": strings.TrimSpace(f.azAccount),
			"storageKey":     f.azKey,
			"prefix":         strings.TrimSpace(f.azPrefix),
		}
		if sas := strings.TrimSpace(f.azSASToken); sas != "" {
			azCfg["sasToken"] = sas
		}
		if dom := strings.TrimSpace(f.azDomain); dom != "" {
			azCfg["storageDomain"] = dom
		}
		req.Storage = kopia.StorageConnection{
			Type:   "azureBlob",
			Config: azCfg,
		}
	}

	return req, nil
}
