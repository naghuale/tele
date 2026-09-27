//go:build !telecli_dev

package telegram

// developmentBuild is false in release builds: the repository checkout
// candidate is never searched.
const developmentBuild = false
