package main

import (
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

func forwardRequest(c *gin.Context, name, destination, destMethod string) {
	proxyReq, err := http.NewRequestWithContext(c.Request.Context(), destMethod, destination, c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("failed to create request: %v", err)})
		return
	}

	for key, values := range c.Request.Header {
		for _, v := range values {
			proxyReq.Header.Add(key, v)
		}
	}

	resp, err := http.DefaultClient.Do(proxyReq)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("failed to reach destination: %v", err)})
		return
	}
	defer resp.Body.Close()

	for key, values := range resp.Header {
		for _, v := range values {
			c.Header(key, v)
		}
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("failed to read response: %v", err)})
		return
	}

	fmt.Printf("[%s] %s %s -> %s %s (%d)\n", name, c.Request.Method, c.Request.URL.Path, destMethod, destination, resp.StatusCode)
	c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), body)
}

func main() {
	viper.SetConfigName("config")
	viper.AddConfigPath(".")

	err := viper.ReadInConfig()
	if err != nil {
		fmt.Println("Error reading config file:", err)
		return
	}

	fmt.Println("Config file loaded successfully")

	r := gin.Default()

	r.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "pong"})
	})

	for _, webhook := range viper.Get("config.webhooks").([]interface{}) {
		wh := webhook.(map[string]interface{})
		name := wh["name"].(string)
		source := wh["source"].(string)
		sourceMethod := wh["source_method"].(string)
		destinations := wh["destinations"].([]interface{})

		destMethod := sourceMethod
		if dm, ok := wh["destination_method"].(string); ok && dm != "" {
			destMethod = dm
		}

		r.Handle(sourceMethod, source, func(c *gin.Context) {
			for index, element := range destinations {
				destination := element.(string)
				forwardRequest(c, name, destination, destMethod)
			}
		})
	}

	r.Run(fmt.Sprintf(":%d", viper.GetInt("config.port")))
}