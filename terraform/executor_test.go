package terraform_test

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudfoundry/bosh-bootloader/fakes"
	"github.com/cloudfoundry/bosh-bootloader/terraform"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Executor", func() {
	var (
		cmd        *fakes.TerraformCmd
		stateStore *fakes.StateStore
		executor   terraform.Executor

		tempDir      string
		terraformDir string
		varsDir      string
		input        map[string]interface{}

		tfStatePath       string
		relativeStatePath string

		tfVarsPath       string
		relativeVarsPath string
	)

	BeforeEach(func() {
		cmd = &fakes.TerraformCmd{}
		stateStore = &fakes.StateStore{}

		executor = terraform.NewExecutor(cmd, stateStore, true)

		var err error
		tempDir, err = ioutil.TempDir("", "")
		Expect(err).NotTo(HaveOccurred())

		terraformDir, err = ioutil.TempDir("", "terraform")
		Expect(err).NotTo(HaveOccurred())
		stateStore.GetTerraformDirCall.Returns.Directory = terraformDir

		varsDir, err = ioutil.TempDir("", "vars")
		Expect(err).NotTo(HaveOccurred())
		stateStore.GetVarsDirCall.Returns.Directory = varsDir

		tfStatePath = filepath.Join(varsDir, "terraform.tfstate")
		relativeStatePath, err = filepath.Rel(terraformDir, tfStatePath)
		Expect(err).NotTo(HaveOccurred())

		tfVarsPath = filepath.Join(varsDir, "terraform.tfvars")
		relativeVarsPath, err = filepath.Rel(terraformDir, tfVarsPath)
		Expect(err).NotTo(HaveOccurred())

		input = map[string]interface{}{
			"availability_zones":          []string{"z1", "z2"},
			"env_id":                      "some-env-id",
			"project_id":                  "some-project-id",
			"region":                      "some-region",
			"zone":                        "some-zone",
			"credentials":                 "some/credentials/path",
			"system_domain":               "some-domain",
			"ssl_certificate":             "some/certificate/path",
			"ssl_certificate_private_key": "some/key/path",
		}
	})

	AfterEach(func() {
		terraform.ResetReadFile()
		terraform.ResetWriteFile()
	})

	Describe("IsInitialized", func() {
		Context("when the state-dir does not contain any terraform files", func() {
			It("returns false", func() {
				Expect(executor.IsInitialized()).To(BeFalse())
			})

			Context("when the state-dir contains all the terraform files", func() {
				BeforeEach(func() {
					err := ioutil.WriteFile(tfStatePath, []byte("state"), os.ModePerm)
					Expect(err).NotTo(HaveOccurred())
					err = ioutil.WriteFile(tfVarsPath, []byte("vars"), os.ModePerm)
					Expect(err).NotTo(HaveOccurred())
					err = ioutil.WriteFile(filepath.Join(terraformDir, "template.tf"), []byte("template"), os.ModePerm)
					Expect(err).NotTo(HaveOccurred())
					err = os.Mkdir(filepath.Join(terraformDir, ".terraform"), os.ModePerm)
					Expect(err).NotTo(HaveOccurred())
				})
				It("returns true", func() {
					Expect(executor.IsInitialized()).To(BeTrue())
				})
			})
		})
	})

	Describe("Init", func() {
		It("writes existing terraform state and runs terraform init", func() {
			err := executor.Init("some-template", "some-tf-state", input)
			Expect(err).NotTo(HaveOccurred())

			Expect(stateStore.GetTerraformDirCall.CallCount).To(Equal(1))

			terraformTemplate, err := ioutil.ReadFile(filepath.Join(terraformDir, "template.tf"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(terraformTemplate)).To(Equal("some-template"))

			terraformState, err := ioutil.ReadFile(tfStatePath)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(terraformState)).To(Equal("some-tf-state"))

			terraformVars, err := ioutil.ReadFile(tfVarsPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(terraformVars)).To(ContainSubstring(`availability_zones=["z1","z2"]`))
			Expect(string(terraformVars)).To(ContainSubstring(`env_id="some-env-id"`))
			Expect(string(terraformVars)).To(ContainSubstring(`project_id="some-project-id"`))
			Expect(string(terraformVars)).To(ContainSubstring(`region="some-region"`))
			Expect(string(terraformVars)).To(ContainSubstring(`zone="some-zone"`))
			Expect(string(terraformVars)).To(ContainSubstring(`credentials="some/credentials/path"`))
			Expect(string(terraformVars)).To(ContainSubstring(`system_domain="some-domain"`))
			Expect(string(terraformVars)).To(ContainSubstring(`ssl_certificate="some/certificate/path"`))
			Expect(string(terraformVars)).To(ContainSubstring(`ssl_certificate_private_key="some/key/path"`))
		})

		It("writes a .gitignore file to .terraform so that plugin binaries are not committed", func() {
			err := executor.Init("some-template", "some-tf-state", input)
			Expect(err).NotTo(HaveOccurred())

			contents, err := ioutil.ReadFile(filepath.Join(terraformDir, ".terraform", ".gitignore"))
			Expect(err).NotTo(HaveOccurred())

			Expect(string(contents)).To(Equal("*\n"))
		})

		Context("when previous tf state is blank", func() {
			var writeTFStateFileCallCount int

			BeforeEach(func() {
				cmd.RunCall.Stub = func(stdout io.Writer) {
					err := ioutil.WriteFile(tfStatePath, []byte("some-tfstate"), os.ModePerm)
					Expect(err).NotTo(HaveOccurred())
				}

				terraform.SetWriteFile(func(file string, data []byte, perm os.FileMode) error {
					if file == "terraform.tfstate" {
						writeTFStateFileCallCount++
					}
					return nil
				})
			})
			AfterEach(func() {
				terraform.ResetWriteFile()
			})

			It("does not write the previous tf state file", func() {
				err := executor.Init("some-template", "", input)
				Expect(err).NotTo(HaveOccurred())

				Expect(writeTFStateFileCallCount).To(Equal(0))
			})
		})

		Context("when an error occurs", func() {
			Context("when getting terraform dir fails", func() {
				BeforeEach(func() {
					stateStore.GetTerraformDirCall.Returns.Error = errors.New("canteloupe")
				})

				It("returns an error", func() {
					err := executor.Init("some-template", "", input)
					Expect(err).To(MatchError("Get terraform dir: canteloupe"))
				})
			})

			Context("when writing the template file fails", func() {
				BeforeEach(func() {
					terraform.SetWriteFile(func(file string, data []byte, perm os.FileMode) error {
						if file == filepath.Join(terraformDir, "template.tf") {
							return errors.New("pear")
						}
						return nil
					})
				})

				It("returns an error", func() {
					err := executor.Init("some-template", "", input)
					Expect(err).To(MatchError("Write terraform template: pear"))
				})
			})

			Context("when getting vars dir fails", func() {
				BeforeEach(func() {
					stateStore.GetVarsDirCall.Returns.Error = errors.New("coconut")
				})

				It("returns an error", func() {
					err := executor.Init("", "", input)
					Expect(err).To(MatchError("Get vars dir: coconut"))
				})
			})

			Context("when writing the previous tfstate file fails", func() {
				BeforeEach(func() {
					terraform.SetWriteFile(func(file string, data []byte, perm os.FileMode) error {
						if file == tfStatePath {
							return errors.New("peach")
						}
						return nil
					})
				})

				It("returns an error", func() {
					err := executor.Init("some-template", "some-tf-state", input)
					Expect(err).To(MatchError("Write previous terraform state: peach"))
				})
			})

			Context("when writing the vars file fails", func() {
				BeforeEach(func() {
					terraform.SetWriteFile(func(file string, data []byte, perm os.FileMode) error {
						if file == tfVarsPath {
							return errors.New("apple")
						}
						return nil
					})
				})

				It("returns an error", func() {
					err := executor.Init("some-template", "", input)
					Expect(err).To(MatchError("Write terraform vars: apple"))
				})
			})

			Context("when creating the .terraform directory fails", func() {
				BeforeEach(func() {
					_, err := os.Create(filepath.Join(terraformDir, ".terraform"))
					Expect(err).NotTo(HaveOccurred())
				})

				It("returns an error", func() {
					err := executor.Init("some-template", "some-tf-state", input)
					Expect(err.Error()).To(ContainSubstring("Create .terraform directory: "))
				})
			})

			Context("when writing the .gitignore for terraform binaries fails", func() {
				BeforeEach(func() {
					terraform.SetWriteFile(func(file string, data []byte, perm os.FileMode) error {
						if file == filepath.Join(terraformDir, ".terraform", ".gitignore") {
							return errors.New("nectarine")
						}
						return nil
					})
				})

				It("returns an error", func() {
					err := executor.Init("some-template", "some-tf-state", input)
					Expect(err).To(MatchError("Write .gitignore for terraform binaries: nectarine"))
				})
			})

			Context("when terraform init fails", func() {
				BeforeEach(func() {
					cmd.RunCall.Returns.Errors = []error{errors.New("guava")}
				})

				It("returns an error", func() {
					err := executor.Init("some-template", "", input)
					Expect(err).To(MatchError("Run terraform init: guava"))
				})
			})
		})
	})

	Describe("Apply", func() {
		It("runs terraform apply", func() {
			terraform.SetReadFile(func(filePath string) ([]byte, error) {
				if strings.Contains(filePath, "tfstate") {
					return []byte("some-updated-terraform-state"), nil
				}
				if strings.Contains(filePath, "tfvars") {
					return []byte("some-tfvars"), nil
				}
				return []byte("some-other-file"), nil
			})

			err := executor.Init("some-template", "some-terraform-state", input) // We need to run the terraform init command.
			Expect(err).NotTo(HaveOccurred())

			terraformState, err := executor.Apply()
			Expect(err).NotTo(HaveOccurred())

			By("passing the correct args and dir to run command", func() {
				Expect(cmd.RunCall.Receives.WorkingDirectory).To(Equal(terraformDir))
				Expect(cmd.RunCall.Receives.Args).To(ConsistOf([]string{
					"apply",
					"-state", relativeStatePath,
					"-var-file", relativeVarsPath,
				}))
				Expect(cmd.RunCall.Receives.Debug).To(BeTrue())
			})

			Expect(terraformState).To(Equal("some-updated-terraform-state"))
		})

		Context("when an error occurs", func() {
			Context("when terraform command run fails", func() {
				BeforeEach(func() {
					err := ioutil.WriteFile(tfStatePath, []byte("some-tf-state"), os.ModePerm)
					Expect(err).NotTo(HaveOccurred())

					cmd.RunCall.Returns.Errors = []error{nil, errors.New("the-executor-error")}
					_ = executor.Init("some-template", "", input)
				})

				It("returns an error and the current tf state", func() {
					_, err := executor.Apply()
					taErr := err.(terraform.ExecutorError)
					Expect(taErr).To(MatchError("the-executor-error"))

					tfState, err := taErr.TFState()
					Expect(err).NotTo(HaveOccurred())
					Expect(tfState).To(Equal("some-tf-state"))
				})
			})

			Context("when reading the tfstate file fails", func() {
				BeforeEach(func() {
					terraform.SetReadFile(func(filename string) ([]byte, error) {
						return []byte{}, errors.New("lychee")
					})
					_ = executor.Init("some-template", "", input)
				})

				It("returns an error", func() {
					_, err := executor.Apply()
					Expect(err).To(MatchError("Read terraform state: lychee"))
				})
			})

			Context("when --debug is false", func() {
				BeforeEach(func() {
					executor = terraform.NewExecutor(cmd, stateStore, false)
				})

				Context("when terraform command run fails", func() {
					BeforeEach(func() {
						err := ioutil.WriteFile(tfStatePath, []byte("some-tf-state"), os.ModePerm)
						Expect(err).NotTo(HaveOccurred())

						cmd.RunCall.Returns.Errors = []error{nil, errors.New("failed to run terraform command")}
					})

					It("returns an error and the current tf state", func() {
						err := executor.Init("some-template", "", input)
						_, err = executor.Apply()
						taErr := err.(terraform.ExecutorError)

						tfState, err := taErr.TFState()
						Expect(err).NotTo(HaveOccurred())
						Expect(tfState).To(Equal("some-tf-state"))
					})
				})
			})
		})
	})

	Describe("Destroy", func() {
		BeforeEach(func() {
			err := executor.Init("some-template", "some-tf-state", input) // We need to run the terraform init command.
			Expect(err).NotTo(HaveOccurred())
		})

		It("writes the template and tf state to a temp dir", func() {
			terraformState, err := executor.Destroy(input)
			Expect(err).NotTo(HaveOccurred())

			Expect(terraformState).To(Equal("some-tf-state"))

			By("passing the correct args and dir to run command", func() {
				Expect(cmd.RunCall.Receives.WorkingDirectory).To(Equal(terraformDir))
				Expect(cmd.RunCall.Receives.Args).To(ConsistOf([]string{
					"destroy",
					"-force",
					"-state", relativeStatePath,
					"-var-file", relativeVarsPath,
				}))
				Expect(cmd.RunCall.Receives.Debug).To(BeTrue())
			})
		})

		Context("when an error occurs", func() {
			Context("when getting terraform dir fails", func() {
				BeforeEach(func() {
					stateStore.GetTerraformDirCall.Returns.Error = errors.New("kiwi")
				})

				It("returns an error", func() {
					_, err := executor.Destroy(input)
					Expect(err).To(MatchError("Get terraform dir: kiwi"))
				})
			})

			Context("when getting vars dir fails", func() {
				BeforeEach(func() {
					stateStore.GetVarsDirCall.Returns.Error = errors.New("banana")
				})

				It("returns an error", func() {
					_, err := executor.Destroy(input)
					Expect(err).To(MatchError("Get vars dir: banana"))
				})
			})

			Context("when it fails to call terraform command run", func() {
				BeforeEach(func() {
					err := ioutil.WriteFile(tfStatePath, []byte("some-tf-state"), os.ModePerm)
					Expect(err).NotTo(HaveOccurred())
					cmd.RunCall.Returns.Errors = []error{nil, errors.New("the-executor-error")}
				})

				It("returns an error and the current tf state", func() {
					_, err := executor.Destroy(input)
					tdErr := err.(terraform.ExecutorError)
					Expect(tdErr).To(MatchError("the-executor-error"))

					tfState, err := tdErr.TFState()
					Expect(err).NotTo(HaveOccurred())
					Expect(tfState).To(Equal("some-tf-state"))
				})
			})

			Context("when it fails to read out the resulting tf state", func() {
				BeforeEach(func() {
					terraform.SetReadFile(func(filename string) ([]byte, error) {
						return []byte{}, errors.New("blueberry")
					})
				})

				It("returns an error", func() {
					_, err := executor.Destroy(input)
					Expect(err).To(MatchError("Read terraform state: blueberry"))
				})
			})

			Context("when --debug is false", func() {
				BeforeEach(func() {
					executor = terraform.NewExecutor(cmd, stateStore, false)
				})

				Context("when it fails to call terraform command run", func() {
					BeforeEach(func() {
						err := ioutil.WriteFile(tfStatePath, []byte("some-tf-state"), os.ModePerm)
						Expect(err).NotTo(HaveOccurred())

						cmd.RunCall.Returns.Errors = []error{nil, errors.New("failed to run terraform command")}
					})

					It("returns an error and the current tf state", func() {
						_, err := executor.Destroy(input)
						tdErr := err.(terraform.ExecutorError)

						tfState, err := tdErr.TFState()
						Expect(tfState).To(Equal("some-tf-state"))
					})
				})
			})
		})
	})

	Describe("Version", func() {
		BeforeEach(func() {
			cmd.RunCall.Stub = func(stdout io.Writer) {
				stdout.Write([]byte("some-text v0.8.9 some-other-text"))
			}
		})

		It("passes the correct args and dir to run command", func() {
			_, err := executor.Version()
			Expect(err).NotTo(HaveOccurred())

			Expect(cmd.RunCall.Receives.Args).To(Equal([]string{"version"}))
			Expect(cmd.RunCall.Receives.Debug).To(BeTrue())
		})

		It("returns the correctly trimmed version", func() {
			version, err := executor.Version()
			Expect(err).NotTo(HaveOccurred())
			Expect(version).To(Equal("0.8.9"))
		})

		Context("when an error occurs", func() {
			Context("when the run command fails", func() {
				BeforeEach(func() {
					cmd.RunCall.Returns.Errors = []error{errors.New("run cmd failed")}
				})

				It("returns an error", func() {
					_, err := executor.Version()
					Expect(err).To(MatchError("run cmd failed"))
				})
			})

			Context("when the version cannot be parsed", func() {
				BeforeEach(func() {
					cmd.RunCall.Stub = func(stdout io.Writer) {
						stdout.Write([]byte(""))
					}
				})

				It("returns an error", func() {
					_, err := executor.Version()
					Expect(err).To(MatchError("Terraform version could not be parsed"))
				})
			})
		})
	})

	Describe("Output", func() {
		It("returns an output from the terraform state", func() {
			cmd.RunCall.Stub = func(stdout io.Writer) {
				fmt.Fprintf(stdout, "some-external-ip\n")
			}
			output, err := executor.Output("some-tf-state", "external_ip")
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(Equal("some-external-ip"))

			Expect(cmd.RunCall.Receives.WorkingDirectory).To(Equal(terraformDir))
			Expect(cmd.RunCall.Receives.Args).To(Equal([]string{"output", "external_ip", "-state", tfStatePath}))
			Expect(cmd.RunCall.Receives.Debug).To(BeTrue())
		})

		Context("when an error occurs", func() {
			Context("when it fails to get terraform dir", func() {
				BeforeEach(func() {
					stateStore.GetTerraformDirCall.Returns.Error = errors.New("failed")
				})

				It("returns an error", func() {
					_, err := executor.Output("some-tf-state", "external_ip")
					Expect(err).To(MatchError("Get terraform dir: failed"))
				})
			})

			Context("when it fails to get vars dir", func() {
				BeforeEach(func() {
					stateStore.GetVarsDirCall.Returns.Error = errors.New("failed")
				})

				It("returns an error", func() {
					_, err := executor.Output("some-tf-state", "external_ip")
					Expect(err).To(MatchError("Get vars dir: failed"))
				})
			})

			Context("when it fails to write the tfstate file", func() {
				BeforeEach(func() {
					terraform.SetWriteFile(func(file string, data []byte, perm os.FileMode) error {
						if strings.Contains(file, "terraform.tfstate") {
							return errors.New("failed")
						}
						return nil
					})
				})

				It("returns an error", func() {
					_, err := executor.Output("some-tf-state", "external_ip")
					Expect(err).To(MatchError("Write terraform state to terraform.tfstate in terraform dir: failed"))
				})
			})

			Context("when terraform init fails", func() {
				BeforeEach(func() {
					cmd.RunCall.Returns.Errors = []error{errors.New("failed")}
				})

				It("returns an error", func() {
					_, err := executor.Output("some-template", "external_ip")
					Expect(err).To(MatchError("Run terraform init in terraform dir: failed"))
				})
			})

			Context("when it fails to call terraform command run", func() {
				BeforeEach(func() {
					cmd.RunCall.Returns.Errors = []error{nil, errors.New("failed")}
				})

				It("returns an error", func() {
					_, err := executor.Output("some-tf-state", "external_ip")
					Expect(err).To(MatchError("Run terraform output -state: failed"))
				})
			})
		})
	})

	Describe("Outputs", func() {
		It("returns all outputs from the terraform state", func() {
			cmd.RunCall.Stub = func(stdout io.Writer) {
				fmt.Fprintf(stdout, `{
					"director_address": {
						"sensitive": false,
						"type": "string",
						"value": "some-director-address"
					},
					"external_ip": {
						"sensitive": false,
						"type": "string",
						"value": "some-external-ip"
					}
				}`)
			}
			outputs, err := executor.Outputs("some-tf-state")
			Expect(err).NotTo(HaveOccurred())

			Expect(outputs).To(Equal(map[string]interface{}{
				"director_address": "some-director-address",
				"external_ip":      "some-external-ip",
			}))

			Expect(cmd.RunCall.Receives.WorkingDirectory).To(Equal(varsDir))
			Expect(cmd.RunCall.Receives.Args).To(Equal([]string{"output", "--json"}))
			Expect(cmd.RunCall.Receives.Debug).To(BeTrue())
		})

		Context("when an error occurs", func() {
			Context("when it fails to get vars dir", func() {
				BeforeEach(func() {
					stateStore.GetVarsDirCall.Returns.Error = errors.New("failed")
				})

				It("returns an error", func() {
					_, err := executor.Outputs("some-tf-state")
					Expect(err).To(MatchError("Get vars dir: failed"))
				})
			})

			Context("when it fails to write the tfstate file", func() {
				BeforeEach(func() {
					terraform.SetWriteFile(func(file string, data []byte, perm os.FileMode) error {
						if strings.Contains(file, "terraform.tfstate") {
							return errors.New("failed")
						}
						return nil
					})
				})

				It("returns an error", func() {
					_, err := executor.Outputs("some-tf-state")
					Expect(err).To(MatchError("Write terraform state to terraform.tfstate: failed"))
				})
			})

			Context("when terraform init fails", func() {
				BeforeEach(func() {
					cmd.RunCall.Returns.Errors = []error{errors.New("failed")}
				})

				It("returns an error", func() {
					_, err := executor.Outputs("some-tf-state")
					Expect(err).To(MatchError("Run terraform init in vars dir: failed"))
				})
			})

			Context("when it fails to call terraform command run", func() {
				BeforeEach(func() {
					cmd.RunCall.Returns.Errors = []error{nil, errors.New("failed")}
				})

				It("returns an error", func() {
					_, err := executor.Outputs("some-tf-state")
					Expect(err).To(MatchError("Run terraform output --json in vars dir: failed"))
				})
			})

			Context("when it fails to unmarshal the terraform outputs", func() {
				BeforeEach(func() {
					cmd.RunCall.Stub = func(stdout io.Writer) {
						fmt.Fprintf(stdout, "%%%")
					}
				})

				It("returns an error", func() {
					_, err := executor.Outputs("some-tf-state")
					Expect(err).To(MatchError("Unmarshal terraform output: invalid character '%' looking for beginning of value"))
				})
			})
		})
	})
})
