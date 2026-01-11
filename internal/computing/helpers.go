package computing

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/filswan/go-mcs-sdk/mcs/api/common/logs"
	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v2"
)

// YamlStruct represents the structure for YAML deployment content
type YamlStruct struct {
	Services struct {
		Image      string            `yaml:"image"`
		Cmd        []string          `yaml:"command"`
		ExposePort []int             `yaml:"expose"`
		Envs       map[string]string `yaml:"environment"`
	} `yaml:"services"`
}

// handlerYamlStr parses YAML content string into YamlStruct
func handlerYamlStr(yamlContent string) (*YamlStruct, error) {
	var yamlStruct YamlStruct
	if err := yaml.Unmarshal([]byte(yamlContent), &yamlStruct); err != nil {
		return nil, fmt.Errorf("failed to parse yaml content: %w", err)
	}
	return &yamlStruct, nil
}

func generateString(length int) string {
	characters := "abcdefghijklmnopqrstuvwxyz"
	numbers := "0123456789"
	source := characters + numbers
	result := make([]byte, length)
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := 0; i < length; i++ {
		result[i] = source[r.Intn(len(source))]
	}
	return string(result)
}

func verifySignature(pubKStr, data, signature string) (bool, error) {
	sb, err := hexutil.Decode(signature)
	if err != nil {
		return false, err
	}
	hash := crypto.Keccak256Hash([]byte(data))
	sigPublicKeyECDSA, err := crypto.SigToPub(hash.Bytes(), sb)
	if err != nil {
		return false, err
	}
	pub := crypto.PubkeyToAddress(*sigPublicKeyECDSA).Hex()
	if pubKStr != pub {
		return false, err
	}
	return true, nil
}

func convertGpuName(name string) string {
	if strings.TrimSpace(name) == "" {
		return ""
	} else {
		name = strings.Split(name, ":")[0]
	}
	if strings.Contains(name, "NVIDIA") {
		if strings.Contains(name, "Tesla") {
			return strings.Replace(name, "Tesla ", "", 1)
		}

		if strings.Contains(name, "GeForce") {
			name = strings.Replace(name, "GeForce ", "", 1)
		}
		return strings.Replace(name, "RTX ", "", 1)
	} else {
		if strings.Contains(name, "GeForce") {
			cpName := strings.Replace(name, "GeForce ", "NVIDIA", 1)
			return strings.Replace(cpName, "RTX", "", 1)
		}
	}
	return strings.ToUpper(name)
}

var regionCache string

func getLocation() (string, error) {
	var err error
	if regionCache != "" {
		return regionCache, nil
	}
	regionCache, err = getRegionByIpInfo()
	if err != nil {
		regionCache, err = getRegionByIpApi()
		if err != nil {
			logs.GetLogger().Errorf("get region info failed, error: %+v", err)
			return "", err
		}
	}
	return regionCache, nil
}

func getRegionByIpApi() (string, error) {
	req, err := http.NewRequest("GET", "https://ipapi.co/ip", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/90.0.4430.212 Safari/537.36")

	client := http.DefaultClient
	IpResp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer IpResp.Body.Close()

	ipBytes, err := io.ReadAll(IpResp.Body)
	if err != nil {
		return "", err
	}

	regionResp, err := http.Get("http://ip-api.com/json/" + string(ipBytes))
	if err != nil {
		return "", err
	}
	defer regionResp.Body.Close()

	body, err := io.ReadAll(regionResp.Body)
	if err != nil {
		return "", err
	}

	var ipInfo struct {
		Country     string `json:"country"`
		CountryCode string `json:"countryCode"`
		City        string `json:"city"`
		Region      string `json:"region"`
		RegionName  string `json:"regionName"`
	}
	if err = json.Unmarshal(body, &ipInfo); err != nil {
		return "", err
	}
	region := strings.TrimSpace(ipInfo.RegionName) + "-" + ipInfo.CountryCode
	return region, nil
}

func getRegionByIpInfo() (string, error) {
	req, err := http.NewRequest("GET", "https://ipinfo.io", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/90.0.4430.212 Safari/537.36")

	client := http.DefaultClient
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	ipBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var ipInfo struct {
		Ip      string `json:"ip"`
		City    string `json:"city"`
		Region  string `json:"region"`
		Country string `json:"country"`
	}
	if err = json.Unmarshal(ipBytes, &ipInfo); err != nil {
		return "", err
	}
	region := strings.TrimSpace(ipInfo.Region) + "-" + ipInfo.Country
	return region, nil
}

// GetPrice is a HTTP handler that returns the current pricing configuration
func GetPrice(c *gin.Context) {
	priceConfig, err := ReadPriceConfig()
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, priceConfig)
}

func getWalletList(walletUrl string) ([]string, error) {
	if walletUrl == "" {
		return nil, fmt.Errorf("wallet url is empty")
	}
	resp, err := http.Get(walletUrl)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var wallets []string
	if err = json.Unmarshal(body, &wallets); err != nil {
		return nil, err
	}
	return wallets, nil
}
