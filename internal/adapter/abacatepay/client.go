package abacatepay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/port"
)

const DefaultBaseURL = "https://api.abacatepay.com/v2"

type Client struct {
	apiKey    string
	productID string
	baseURL   string
	http      *http.Client
}

func NewClient(apiKey string, productID string) *Client {
	return &Client{
		apiKey:    apiKey,
		productID: productID,
		baseURL:   DefaultBaseURL,
		http: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (c *Client) SetBaseURL(url string) {
	c.baseURL = url
}

type apiResponse struct {
	Data    json.RawMessage `json:"data"`
	Error   json.RawMessage `json:"error"`
	Success bool            `json:"success"`
}

type createCustomerRequest struct {
	Email string `json:"email"`
}

type customerData struct {
	ID string `json:"id"`
}

func (c *Client) CreateCustomer(ctx context.Context, email string) (string, error) {
	body := createCustomerRequest{Email: email}
	resp, err := c.doRequest(ctx, http.MethodPost, "/customers/create", body)
	if err != nil {
		return "", fmt.Errorf("create customer: %w", err)
	}
	var data customerData
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		return "", fmt.Errorf("parse customer response: %w", err)
	}
	return data.ID, nil
}

type createSubscriptionRequest struct {
	Items         []subscriptionRequestItem `json:"items"`
	CustomerID    string                    `json:"customerId,omitempty"`
	ExternalID    string                    `json:"externalId,omitempty"`
	ReturnURL     string                    `json:"returnUrl,omitempty"`
	CompletionURL string                    `json:"completionUrl,omitempty"`
	Methods       []string                  `json:"methods,omitempty"`
	Metadata      map[string]string         `json:"metadata,omitempty"`
}

type subscriptionRequestItem struct {
	ID       string `json:"id"`
	Quantity int    `json:"quantity"`
}

type subscriptionCheckoutData struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

func (c *Client) CreateSubscriptionCheckout(ctx context.Context, input port.SubscriptionCheckoutInput) (*port.SubscriptionCheckout, error) {
	body := createSubscriptionRequest{
		Items: []subscriptionRequestItem{
			{ID: c.productID, Quantity: 1},
		},
		CustomerID:    input.CustomerID,
		ExternalID:    input.ExternalID,
		ReturnURL:     input.ReturnURL,
		CompletionURL: input.CompletionURL,
		Methods:       []string{"CARD"},
		Metadata:      input.Metadata,
	}
	resp, err := c.doRequest(ctx, http.MethodPost, "/subscriptions/create", body)
	if err != nil {
		return nil, fmt.Errorf("create subscription checkout: %w", err)
	}
	var data subscriptionCheckoutData
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		return nil, fmt.Errorf("parse subscription response: %w", err)
	}
	if data.ID == "" || data.URL == "" {
		return nil, fmt.Errorf("subscription response missing id or url")
	}
	return &port.SubscriptionCheckout{ID: data.ID, URL: data.URL}, nil
}

func (c *Client) doRequest(ctx context.Context, method string, path string, body any) (*apiResponse, error) {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("api error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var apiResp apiResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if len(apiResp.Error) > 0 && string(apiResp.Error) != "null" {
		return nil, fmt.Errorf("api error: %s", string(apiResp.Error))
	}

	return &apiResp, nil
}
