package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

// forwardRequest executes the network call to a single destination.
// It returns the status code, response body, and any network/read error.
func forwardRequest(ctx context.Context, incomingHeaders http.Header, name, destination, destMethod string, bodyBytes []byte) (int, []byte, error) {
	// Create a fresh reader of the cached body bytes for this destination
	proxyReq, err := http.NewRequestWithContext(ctx, destMethod, destination, bytes.NewReader(bodyBytes))
	if err != nil {
		return http.StatusBadGateway, nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Copy incoming headers from the original client request
	for key, values := range incomingHeaders {
		for _, v := range values {
			proxyReq.Header.Add(key, v)
		}
	}

	// Execute the request
	resp, err := http.DefaultClient.Do(proxyReq)
	if err != nil {
		return http.StatusBadGateway, nil, fmt.Errorf("failed to reach destination: %w", err)
	}
	defer resp.Body.Close()

	// Read downstream response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return http.StatusBadGateway, nil, fmt.Errorf("failed to read response: %w", err)
	}

	fmt.Printf("[%s] Forwarded to -> %s %s (%d)\n", name, destMethod, destination, resp.StatusCode)
	return resp.StatusCode, body, nil
}

func main() {
	viper.SetConfigName("config")
	viper.AddConfigPath(".")

	err := viper.ReadInConfig()
	if err != nil {
		log.Fatalf("Error reading config file: %v", err)
	}

	fmt.Println("Config file loaded successfully")

	r := gin.Default()

	r.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "pong"})
	})

	// --- DEFENSIVE CONFIGURATION PARSING ---
	rawWebhooks := viper.Get("config.webhooks")
	if rawWebhooks == nil {
		log.Fatal("Error: 'config.webhooks' key missing from configuration file.")
	}

	webhooksSlice, ok := rawWebhooks.([]interface{})
	if !ok {
		log.Fatalf("Error: 'config.webhooks' is not formatted as a valid list. Found type: %T", rawWebhooks)
	}

	for _, webhook := range webhooksSlice {
		wh, ok := webhook.(map[string]interface{})
		if !ok {
			fmt.Println("Warning: Skipping invalid, non-map element found in webhooks list.")
			continue
		}

		name, _ := wh["name"].(string)
		source, _ := wh["source"].(string)
		sourceMethod, _ := wh["source_method"].(string)
		
		rawDestinations, exists := wh["destinations"]
		if !exists || rawDestinations == nil {
			fmt.Printf("Warning: Webhook '%s' skipped because it has no destinations configured.\n", name)
			continue
		}
		destinations := rawDestinations.([]interface{})

		destMethod := sourceMethod
		if dm, ok := wh["destination_method"].(string); ok && dm != "" {
			destMethod = dm
		}

		// --- HOIST CLOSURE VARIABLES ---
		// We copy variables to local loop block scopes to prevent Gin route 
		// handlers from capturing changing pointer references across loop iterations.
		localName := name
		localSource := source
		localSourceMethod := sourceMethod
		localDestMethod := destMethod

		localDestinations := make([]string, len(destinations))
		for i, element := range destinations {
			localDestinations[i] = element.(string)
		}

		// --- ROUTE REGISTER ---
		r.Handle(localSourceMethod, localSource, func(c *gin.Context) {
			// 1. Sniff and read the incoming body once. 
			// c.Request.Body is a non-rewindable stream, so caching it is required for fan-out architectures.
			bodyBytes, err := io.ReadAll(c.Request.Body)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read incoming request body"})
				return
			}

			var forwardErrors []string
			hasFailures := false

			// 2. Safely fan-out to all target destinations without clobbering gin.Context
			for _, destination := range localDestinations {
				status, _, err := forwardRequest(c.Request.Context(), c.Request.Header, localName, destination, localDestMethod, bodyBytes)
				if err != nil {
					hasFailures = true
					forwardErrors = append(forwardErrors, fmt.Sprintf("[%s]: %v", destination, err))
				} else if status >= 400 {
					hasFailures = true
					forwardErrors = append(forwardErrors, fmt.Sprintf("[%s]: returned status %d", destination, status))
				}
			}

			// 3. Complete the request lifecycle by writing exactly ONCE to Gin
			if hasFailures {
				c.JSON(http.StatusBadGateway, gin.H{
					"status": "partial_or_complete_failure", 
					"errors": forwardErrors,
				})
				return
			}

			c.JSON(http.StatusOK, gin.H{"status": "success", "message": "all destinations notified"})
		})
	}

	port := viper.GetInt("config.port")
	if port == 0 {
		port = 8080 // Fallback default
	}
	r.Run(fmt.Sprintf(":%d", port))
}