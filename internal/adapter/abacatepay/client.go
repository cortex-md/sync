package abacatepay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const defaultBaseURL = "https://api.abacatepay.com/v1"

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
		baseURL:   defaultBaseURL,
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
	Error   string          `json:"error"`
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
	ProductID string   `json:"productId"`
	Customer  string   `json:"customer"`
	Methods   []string `json:"methods"`
	ReturnURL string   `json:"returnUrl"`
}

type subscriptionCheckoutData struct {
	URL string `json:"url"`
}

func (c *Client) CreateSubscriptionCheckout(ctx context.Context, customerID string, returnURL string) (string, error) {
	body := createSubscriptionRequest{
		ProductID: c.productID,
		Customer:  customerID,
		Methods:   []string{"PIX"},
		ReturnURL: returnURL,
	}
	resp, err := c.doRequest(ctx, http.MethodPost, "/subscriptions/create", body)
	if err != nil {
		return "", fmt.Errorf("create subscription checkout: %w", err)
	}
	var data subscriptionCheckoutData
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		return "", fmt.Errorf("parse subscription response: %w", err)
	}
	return data.URL, nil
}

type subscriptionItem struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

func (c *Client) GetSubscriptionStatus(ctx context.Context, subscriptionID string) (string, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/subscriptions/get?id=%s", subscriptionID), nil)
	if err != nil {
		return "", fmt.Errorf("get subscription status: %w", err)
	}
	var data subscriptionItem
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		return "", fmt.Errorf("parse subscription status: %w", err)
	}
	return data.Status, nil
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

	if apiResp.Error != "" {
		return nil, fmt.Errorf("api error: %s", apiResp.Error)
	}

	return &apiResp, nil
}
