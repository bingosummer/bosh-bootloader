package commands

import (
	"errors"
	"fmt"
	"io/ioutil"

	"github.com/cloudfoundry/bosh-bootloader/bosh"
	"github.com/cloudfoundry/bosh-bootloader/storage"
)

type Up struct {
	plan               plan
	boshManager        boshManager
	cloudConfigManager cloudConfigManager
	stateStore         stateStore
	envIDManager       envIDManager
	terraformManager   terraformManager
}

type UpConfig struct {
	Name       string
	OpsFile    string
	NoDirector bool
}

func NewUp(plan plan, boshManager boshManager, cloudConfigManager cloudConfigManager,
	stateStore stateStore, envIDManager envIDManager, terraformManager terraformManager) Up {
	return Up{
		plan:               plan,
		boshManager:        boshManager,
		cloudConfigManager: cloudConfigManager,
		stateStore:         stateStore,
		envIDManager:       envIDManager,
		terraformManager:   terraformManager,
	}
}

func (u Up) CheckFastFails(args []string, state storage.State) error {
	return u.plan.CheckFastFails(args, state)
}

func (u Up) Execute(args []string, state storage.State) error {
	config, err := u.ParseArgs(args, state)
	if err != nil {
		return err
	}

	if config.NoDirector {
		if !state.BOSH.IsEmpty() {
			return errors.New(`Director already exists, you must re-create your environment to use "--no-director"`)
		}

		state.NoDirector = true
	}

	var opsFileContents []byte
	if config.OpsFile != "" {
		opsFileContents, err = ioutil.ReadFile(config.OpsFile)
		if err != nil {
			return fmt.Errorf("Reading ops-file contents: %v", err)
		}
	}

	state, err = u.envIDManager.Sync(state, config.Name)
	if err != nil {
		return fmt.Errorf("Env id manager sync: %s", err)
	}

	err = u.stateStore.Set(state)
	if err != nil {
		return fmt.Errorf("Save state after sync: %s", err)
	}

	if !u.terraformManager.IsInitialized() {
		err = u.terraformManager.Init(state)
		if err != nil {
			return fmt.Errorf("Terraform manager init: %s", err)
		}
	}

	state, err = u.terraformManager.Apply(state)
	if err != nil {
		return handleTerraformError(err, u.stateStore)
	}

	err = u.stateStore.Set(state)
	if err != nil {
		return fmt.Errorf("Save state after terraform apply: %s", err)
	}

	if state.NoDirector {
		return nil
	}

	terraformOutputs, err := u.terraformManager.GetOutputs(state)
	if err != nil {
		return fmt.Errorf("Parse terraform outputs: %s", err)
	}

	if !u.boshManager.IsJumpboxInitialized(state.IAAS) {
		if err := u.boshManager.InitializeJumpbox(state); err != nil {
			return fmt.Errorf("Create jumpbox: %s", err)
		}
	}

	state, err = u.boshManager.CreateJumpbox(state, terraformOutputs)
	if err != nil {
		return fmt.Errorf("Create jumpbox: %s", err)
	}

	err = u.stateStore.Set(state)
	if err != nil {
		return fmt.Errorf("Save state after create jumpbox: %s", err)
	}

	if !u.boshManager.IsDirectorInitialized(state.IAAS) {
		state.BOSH.UserOpsFile = string(opsFileContents)
		if err := u.boshManager.InitializeDirector(state); err != nil {
			return fmt.Errorf("Create bosh director: %s", err)
		}
	}

	state, err = u.boshManager.CreateDirector(state, terraformOutputs)
	switch err.(type) {
	case bosh.ManagerCreateError:
		bcErr := err.(bosh.ManagerCreateError)
		if setErr := u.stateStore.Set(bcErr.State()); setErr != nil {
			return fmt.Errorf("Save state after bosh director create error: %s, %s", err, setErr)
		}
		return fmt.Errorf("Create bosh director: %s", err)
	case error:
		return fmt.Errorf("Create bosh director: %s", err)
	}

	err = u.stateStore.Set(state)
	if err != nil {
		return fmt.Errorf("Save state after create director: %s", err)
	}

	err = u.cloudConfigManager.Update(state)
	if err != nil {
		return fmt.Errorf("Update cloud config: %s", err)
	}

	return nil
}

func (u Up) ParseArgs(args []string, state storage.State) (UpConfig, error) {
	return u.plan.ParseArgs(args, state)
}
