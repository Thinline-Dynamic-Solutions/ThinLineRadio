// Copyright (C) 2025 Thinline Dynamic Solutions
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>

package main

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/smtp"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type EmailService struct {
	Controller *Controller
}

func NewEmailService(controller *Controller) *EmailService {
	return &EmailService{
		Controller: controller,
	}
}

// extractNameFromEmail extracts the name part from an email address
func extractNameFromEmail(email string) string {
	parts := strings.Split(email, "@")
	if len(parts) > 0 {
		return parts[0]
	}
	return email
}

// extractDomainFromEmail extracts the domain part from an email address
func extractDomainFromEmail(email string) string {
	parts := strings.Split(email, "@")
	if len(parts) == 2 {
		return parts[1]
	}
	return "localhost"
}

// generateMessageID generates a unique RFC-compliant Message-ID
func generateMessageID(domain string) string {
	// Generate random bytes for uniqueness
	randomBytes := make([]byte, 16)
	rand.Read(randomBytes)
	randomHex := hex.EncodeToString(randomBytes)

	// Format: <timestamp.random@domain>
	timestamp := time.Now().Unix()
	return fmt.Sprintf("<%d.%s@%s>", timestamp, randomHex, domain)
}

// removeEmojis removes all emoji characters from a string
func removeEmojis(text string) string {
	// Regex to match emoji ranges (most comprehensive coverage)
	emojiRegex := regexp.MustCompile(`[\x{1F300}-\x{1F9FF}]|[\x{2600}-\x{26FF}]|[\x{2700}-\x{27BF}]|[\x{1F900}-\x{1F9FF}]|[\x{1F1E0}-\x{1F1FF}]|[\x{1F600}-\x{1F64F}]|[\x{1F680}-\x{1F6FF}]|[\x{200D}]`)
	return emojiRegex.ReplaceAllString(text, "")
}

// removeEmojisFromHTML removes emojis from HTML content while preserving HTML tags
func removeEmojisFromHTML(htmlContent string) string {
	// Match content between HTML tags and replace emojis in text nodes
	// This regex matches text between > and < (text nodes)
	textNodeRegex := regexp.MustCompile(`>([^<]+)<`)
	result := textNodeRegex.ReplaceAllStringFunc(htmlContent, func(match string) string {
		textContent := match[1 : len(match)-1]
		cleanedText := removeEmojis(textContent)
		return ">" + cleanedText + "<"
	})
	return result
}

// htmlToPlainText converts HTML email body to plain text
func htmlToPlainText(htmlContent string) string {
	// Remove HTML tags
	re := regexp.MustCompile(`<[^>]*>`)
	text := re.ReplaceAllString(htmlContent, "")

	// Decode HTML entities
	text = html.UnescapeString(text)

	// Remove emojis
	text = removeEmojis(text)

	// Clean up whitespace
	text = strings.TrimSpace(text)
	lines := strings.Split(text, "\n")
	var cleanedLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			cleanedLines = append(cleanedLines, trimmed)
		}
	}

	return strings.Join(cleanedLines, "\n\n")
}

// buildEmailMessage builds a properly formatted email message with all required headers
func buildEmailMessage(fromName, fromEmail, toEmail, subject, htmlBody string) string {
	// Remove emojis from subject and body
	subject = removeEmojis(subject)
	htmlBody = removeEmojisFromHTML(htmlBody)
	// Extract domain for HELO and Message-ID
	domain := extractDomainFromEmail(fromEmail)

	// Generate unique Message-ID
	messageID := generateMessageID(domain)

	// Convert HTML to plain text
	plainText := htmlToPlainText(htmlBody)

	// Create multipart boundary
	boundary := fmt.Sprintf("----=_Part_%d_%s", time.Now().Unix(), strings.ReplaceAll(messageID, "@", "_at_"))

	// Build message with proper headers
	var message strings.Builder

	// Standard headers
	message.WriteString(fmt.Sprintf("From: %s <%s>\r\n", fromName, fromEmail))
	message.WriteString(fmt.Sprintf("To: %s\r\n", toEmail))
	message.WriteString(fmt.Sprintf("Reply-To: %s <%s>\r\n", fromName, fromEmail))
	message.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
	message.WriteString(fmt.Sprintf("Message-ID: %s\r\n", messageID))
	message.WriteString(fmt.Sprintf("Date: %s\r\n", time.Now().Format(time.RFC1123Z)))
	message.WriteString("X-Mailer: ThinLine Radio Mail Service\r\n")
	message.WriteString("MIME-Version: 1.0\r\n")
	message.WriteString(fmt.Sprintf("Content-Type: multipart/alternative; boundary=\"%s\"\r\n", boundary))
	message.WriteString("\r\n")

	// Plain text part
	message.WriteString(fmt.Sprintf("--%s\r\n", boundary))
	message.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	message.WriteString("Content-Transfer-Encoding: 7bit\r\n")
	message.WriteString("\r\n")
	message.WriteString(plainText)
	message.WriteString("\r\n\r\n")

	// HTML part
	message.WriteString(fmt.Sprintf("--%s\r\n", boundary))
	message.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	message.WriteString("Content-Transfer-Encoding: 7bit\r\n")
	message.WriteString("\r\n")
	message.WriteString(htmlBody)
	message.WriteString("\r\n\r\n")

	// End boundary
	message.WriteString(fmt.Sprintf("--%s--\r\n", boundary))

	return message.String()
}

// sendSendGridEmail sends an email using the SendGrid API
func (es *EmailService) sendSendGridEmail(fromName, fromEmail, toEmail, subject, htmlBody string) error {
	// Remove emojis from subject and body (already done in sendEmail, but ensure it here too)
	subject = removeEmojis(subject)
	htmlBody = removeEmojisFromHTML(htmlBody)

	apiKey := es.Controller.Options.EmailSendGridAPIKey
	if apiKey == "" {
		return fmt.Errorf("SendGrid API key is not configured")
	}

	plainText := htmlToPlainText(htmlBody)

	// Build SendGrid JSON payload
	payload := map[string]interface{}{
		"personalizations": []map[string]interface{}{
			{
				"to": []map[string]string{
					{"email": toEmail},
				},
				"subject": subject,
			},
		},
		"from": map[string]string{
			"email": fromEmail,
			"name":  fromName,
		},
		"reply_to": map[string]string{
			"email": fromEmail,
			"name":  fromName,
		},
		"content": []map[string]string{
			{
				"type":  "text/plain",
				"value": plainText,
			},
			{
				"type":  "text/html",
				"value": htmlBody,
			},
		},
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal SendGrid payload: %v", err)
	}

	req, err := http.NewRequest("POST", "https://api.sendgrid.com/v3/mail/send", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create SendGrid request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send SendGrid request: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		log.Printf("SendGrid API error: %s (status: %d)", string(body), resp.StatusCode)
		return fmt.Errorf("SendGrid API returned status %d: %s", resp.StatusCode, string(body))
	}

	log.Printf("Email sent successfully via SendGrid to %s", toEmail)
	return nil
}

// sendMailgunEmail sends an email using the Mailgun API
func (es *EmailService) sendMailgunEmail(fromName, fromEmail, toEmail, subject, htmlBody string) error {
	// Remove emojis from subject and body (already done in sendEmail, but ensure it here too)
	subject = removeEmojis(subject)
	htmlBody = removeEmojisFromHTML(htmlBody)

	apiKey := es.Controller.Options.EmailMailgunAPIKey
	domain := es.Controller.Options.EmailMailgunDomain
	apiBase := es.Controller.Options.EmailMailgunAPIBase

	if apiKey == "" {
		return fmt.Errorf("Mailgun API key is not configured")
	}
	if domain == "" {
		return fmt.Errorf("Mailgun domain is not configured")
	}
	if apiBase == "" {
		apiBase = "https://api.mailgun.net"
	}

	plainText := htmlToPlainText(htmlBody)

	// Build Mailgun form data
	data := url.Values{}
	data.Set("from", fmt.Sprintf("%s <%s>", fromName, fromEmail))
	data.Set("to", toEmail)
	data.Set("subject", subject)
	data.Set("text", plainText)
	data.Set("html", htmlBody)

	apiURL := fmt.Sprintf("%s/v3/%s/messages", apiBase, domain)

	req, err := http.NewRequest("POST", apiURL, strings.NewReader(data.Encode()))
	if err != nil {
		return fmt.Errorf("failed to create Mailgun request: %v", err)
	}

	req.SetBasicAuth("api", apiKey)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send Mailgun request: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		log.Printf("Mailgun API error: %s (status: %d)", string(body), resp.StatusCode)
		return fmt.Errorf("Mailgun API returned status %d: %s", resp.StatusCode, string(body))
	}

	log.Printf("Email sent successfully via Mailgun to %s", toEmail)
	return nil
}

// sendSMTPEmail sends an email using direct SMTP connection
func (es *EmailService) sendSMTPEmail(fromName, fromEmail, toEmail, subject, htmlBody string) error {
	// Remove emojis from subject and body (already done in sendEmail, but ensure it here too)
	subject = removeEmojis(subject)
	htmlBody = removeEmojisFromHTML(htmlBody)
	host := es.Controller.Options.EmailSmtpHost
	port := es.Controller.Options.EmailSmtpPort
	username := es.Controller.Options.EmailSmtpUsername
	password := es.Controller.Options.EmailSmtpPassword
	useTLS := es.Controller.Options.EmailSmtpUseTLS
	skipVerify := es.Controller.Options.EmailSmtpSkipVerify

	if host == "" {
		return fmt.Errorf("SMTP host is not configured")
	}
	if port == 0 {
		port = 587 // Default to submission port
	}

	// Build the properly formatted email message
	message := buildEmailMessage(fromName, fromEmail, toEmail, subject, htmlBody)

	// Create the SMTP address
	addr := fmt.Sprintf("%s:%d", host, port)

	// Configure TLS if enabled
	var tlsConfig *tls.Config
	if useTLS {
		tlsConfig = &tls.Config{
			ServerName:         host,
			InsecureSkipVerify: skipVerify,
		}
	}

	// Attempt to send the email
	var err error

	// If using TLS on port 465 (implicit TLS/SSL)
	if useTLS && port == 465 {
		// Use TLS connection from the start (implicit TLS)
		conn, err := tls.Dial("tcp", addr, tlsConfig)
		if err != nil {
			return fmt.Errorf("failed to connect to SMTP server with TLS: %v", err)
		}
		defer conn.Close()

		client, err := smtp.NewClient(conn, host)
		if err != nil {
			return fmt.Errorf("failed to create SMTP client: %v", err)
		}
		defer client.Close()

		// Authenticate if credentials provided
		if username != "" && password != "" {
			auth := smtp.PlainAuth("", username, password, host)
			if err = client.Auth(auth); err != nil {
				return fmt.Errorf("SMTP authentication failed: %v", err)
			}
		}

		// Send the email
		if err = client.Mail(fromEmail); err != nil {
			return fmt.Errorf("SMTP MAIL command failed: %v", err)
		}
		if err = client.Rcpt(toEmail); err != nil {
			return fmt.Errorf("SMTP RCPT command failed: %v", err)
		}

		w, err := client.Data()
		if err != nil {
			return fmt.Errorf("SMTP DATA command failed: %v", err)
		}

		_, err = w.Write([]byte(message))
		if err != nil {
			return fmt.Errorf("failed to write email message: %v", err)
		}

		err = w.Close()
		if err != nil {
			return fmt.Errorf("failed to close SMTP data writer: %v", err)
		}

		err = client.Quit()
		if err != nil {
			log.Printf("SMTP QUIT command warning: %v", err)
		}

		log.Printf("Email sent successfully via SMTP to %s", toEmail)
		return nil
	}

	// For other ports (25, 587, etc.), use STARTTLS if TLS is enabled
	client, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("failed to connect to SMTP server: %v", err)
	}
	defer client.Close()

	// Send HELO/EHLO
	if err = client.Hello(extractDomainFromEmail(fromEmail)); err != nil {
		return fmt.Errorf("SMTP HELLO command failed: %v", err)
	}

	// Use STARTTLS if TLS is enabled and not on port 465
	if useTLS {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err = client.StartTLS(tlsConfig); err != nil {
				return fmt.Errorf("SMTP STARTTLS failed: %v", err)
			}
		}
	}

	// Authenticate if credentials provided
	if username != "" && password != "" {
		auth := smtp.PlainAuth("", username, password, host)
		if err = client.Auth(auth); err != nil {
			return fmt.Errorf("SMTP authentication failed: %v", err)
		}
	}

	// Send the email
	if err = client.Mail(fromEmail); err != nil {
		return fmt.Errorf("SMTP MAIL command failed: %v", err)
	}
	if err = client.Rcpt(toEmail); err != nil {
		return fmt.Errorf("SMTP RCPT command failed: %v", err)
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("SMTP DATA command failed: %v", err)
	}

	_, err = w.Write([]byte(message))
	if err != nil {
		return fmt.Errorf("failed to write email message: %v", err)
	}

	err = w.Close()
	if err != nil {
		return fmt.Errorf("failed to close SMTP data writer: %v", err)
	}

	err = client.Quit()
	if err != nil {
		log.Printf("SMTP QUIT command warning: %v", err)
	}

	log.Printf("Email sent successfully via SMTP to %s", toEmail)
	return nil
}

// sendEmail routes to the appropriate email provider
func (es *EmailService) sendEmail(fromName, fromEmail, toEmail, subject, htmlBody string) error {
	// Remove emojis from subject and body for all emails
	subject = removeEmojis(subject)
	htmlBody = removeEmojisFromHTML(htmlBody)

	provider := es.Controller.Options.EmailProvider
	if provider == "" {
		provider = "sendgrid" // Default to SendGrid
	}

	switch strings.ToLower(provider) {
	case "sendgrid":
		return es.sendSendGridEmail(fromName, fromEmail, toEmail, subject, htmlBody)
	case "mailgun":
		return es.sendMailgunEmail(fromName, fromEmail, toEmail, subject, htmlBody)
	case "smtp":
		return es.sendSMTPEmail(fromName, fromEmail, toEmail, subject, htmlBody)
	default:
		log.Printf("Unknown email provider '%s', must be 'sendgrid', 'mailgun', or 'smtp'", provider)
		return fmt.Errorf("unknown email provider: %s (must be 'sendgrid', 'mailgun', or 'smtp')", provider)
	}
}

// getVerificationEmailHTML generates the HTML content for verification emails
func getVerificationEmailHTML(userEmail, verificationURL, branding, logoURL, borderRadius string, billingEnabled bool) string {
	if branding == "" {
		branding = "ThinLine Radio"
	}

	htmlTemplate := `<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Verify Your Email - {{.Branding}}</title>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif;
            line-height: 1.6;
            color: #333;
            max-width: 600px;
            margin: 0 auto;
            padding: 20px;
            background-color: #f5f5f5;
        }
        .container {
            background-color: #ffffff;
            border-radius: 12px;
            padding: 0;
            box-shadow: 0 4px 12px rgba(0,0,0,0.1);
            overflow: hidden;
        }
        .header {
            background: #2c2c2c;
            color: white;
            text-align: center;
            padding: 40px 20px;
        }
        .logo-img {
            max-width: 200px;
            height: auto;
            margin: 0 auto 15px auto;
            display: block;
            border-radius: {{.BorderRadius}};
        }
        .logo-icon {
            font-size: 64px;
            margin: 0 auto 15px auto;
            display: block;
            text-align: center;
        }
        h1 {
            color: white;
            margin: 0;
            font-size: 28px;
            font-weight: 600;
        }
        .content {
            padding: 40px;
        }
        .content p {
            margin: 0 0 15px 0;
            color: #555;
            font-size: 16px;
        }
        .content p:first-child {
            font-size: 18px;
            color: #333;
        }
        .button-container {
            text-align: center;
            margin: 30px 0;
        }
        .button {
            display: inline-block;
            padding: 16px 40px;
            background: #2c2c2c;
            color: #ffffff;
            text-decoration: none;
            border-radius: 8px;
            font-size: 16px;
            font-weight: 600;
            box-shadow: 0 4px 12px rgba(0, 0, 0, 0.2);
        }
        .button:hover {
            background: #1a1a1a;
        }
        .link-box {
            background-color: #f8f9fa;
            border: 1px solid #e9ecef;
            border-radius: 6px;
            padding: 15px;
            margin: 20px 0;
            word-break: break-all;
        }
        .link {
            color: #2c2c2c;
            text-decoration: none;
            font-size: 14px;
        }
        .expiry-note {
            background-color: #fff3cd;
            border-left: 4px solid #ffc107;
            padding: 12px 15px;
            margin: 20px 0;
            font-size: 14px;
            color: #856404;
        }
        .subscription-info {
            background-color: #e7f3ff;
            border-left: 4px solid #2196F3;
            padding: 15px;
            margin: 20px 0;
            font-size: 15px;
            color: #1976D2;
        }
        .subscription-info strong {
            color: #0d47a1;
        }
        .footer {
            text-align: center;
            padding: 30px 40px;
            background-color: #f8f9fa;
            border-top: 1px solid #e9ecef;
        }
        .footer-icon {
            font-size: 32px;
            margin-bottom: 10px;
        }
        .footer p {
            margin: 5px 0;
            font-size: 13px;
            color: #6c757d;
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            {{if .LogoURL}}
                <img src="{{.LogoURL}}" alt="{{.Branding}}" class="logo-img">
            {{else}}
                <div class="logo-icon">📻</div>
            {{end}}
            <h1>{{.Branding}}</h1>
        </div>
        <div class="content">
            <p><strong>Welcome!</strong></p>
            <p>Thank you for creating an account with {{.Branding}}. To complete your registration and access all features, please verify your email address.</p>
            
            <div class="button-container">
                <a href="{{.VerificationURL}}" class="button">Verify Email Address</a>
            </div>
            
            <p style="text-align: center; color: #6c757d; font-size: 14px;">
                Or copy and paste this link into your browser:
            </p>
            <div class="link-box">
                <a href="{{.VerificationURL}}" class="link">{{.VerificationURL}}</a>
            </div>
            
            <div class="expiry-note">
                ⏰ This verification link will expire in 24 hours.
            </div>
            
            {{if .BillingEnabled}}
            <div class="subscription-info">
                <strong>💳 Subscription Required</strong><br>
                After verifying your email, you will need to log in and complete your subscription to access the service.
            </div>
            {{end}}
            
            <p style="font-size: 14px; color: #6c757d;">
                If you didn't create an account with {{.Branding}}, you can safely ignore this email.
            </p>
        </div>
        <div class="footer">
            {{if .LogoURL}}
                <img src="{{.LogoURL}}" alt="{{.Branding}}" style="max-width: 100px; height: auto; display: block; margin: 0 auto; border-radius: {{.BorderRadius}};">
            {{else}}
                <div class="footer-icon">📻</div>
            {{end}}
        </div>
    </div>
</body>
</html>`

	tmpl, err := template.New("email").Parse(htmlTemplate)
	if err != nil {
		// Fallback to simple HTML if template parsing fails
		return fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head><meta charset="UTF-8"></head>
<body>
    <h1>Verify Your Email</h1>
    <p>Please verify your email by clicking this link: <a href="%s">%s</a></p>
</body>
</html>`, verificationURL, verificationURL)
	}

	var buf bytes.Buffer
	data := struct {
		Branding        string
		VerificationURL string
		LogoURL         string
		BorderRadius    string
		BillingEnabled  bool
	}{
		Branding:        branding,
		VerificationURL: verificationURL,
		LogoURL:         logoURL,
		BorderRadius:    borderRadius,
		BillingEnabled:  billingEnabled,
	}

	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head><meta charset="UTF-8"></head>
<body>
    <h1>Verify Your Email</h1>
    <p>Please verify your email by clicking this link: <a href="%s">%s</a></p>
</body>
</html>`, verificationURL, verificationURL)
	}

	return buf.String()
}

// getEmailChangeVerificationHTML generates the HTML content for email change verification emails
func getEmailChangeVerificationHTML(newEmail, verificationURL, branding, logoURL string) string {
	if branding == "" {
		branding = "ThinLine Radio"
	}

	htmlTemplate := `<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Verify Your New Email Address - {{.Branding}}</title>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif;
            line-height: 1.6;
            color: #333;
            max-width: 600px;
            margin: 0 auto;
            padding: 20px;
            background-color: #f5f5f5;
        }
        .container {
            background-color: #ffffff;
            border-radius: 12px;
            padding: 0;
            box-shadow: 0 4px 12px rgba(0,0,0,0.1);
            overflow: hidden;
        }
        .header {
            background: #2c2c2c;
            color: white;
            text-align: center;
            padding: 40px 20px;
        }
        .logo-img {
            max-width: 200px;
            height: auto;
            margin: 0 auto 15px auto;
            display: block;
        }
        .logo-icon {
            font-size: 64px;
            margin: 0 auto 15px auto;
            display: block;
            text-align: center;
        }
        h1 {
            color: white;
            margin: 0;
            font-size: 28px;
            font-weight: 600;
        }
        .content {
            padding: 40px;
        }
        .content p {
            margin: 0 0 15px 0;
            color: #555;
            font-size: 16px;
        }
        .content p:first-child {
            font-size: 18px;
            color: #333;
        }
        .email-highlight {
            background-color: #e7f3ff;
            border-left: 4px solid #2196F3;
            padding: 12px 15px;
            margin: 20px 0;
            font-size: 16px;
            color: #1976D2;
            font-weight: 600;
        }
        .button-container {
            text-align: center;
            margin: 30px 0;
        }
        .button {
            display: inline-block;
            padding: 16px 40px;
            background: #2c2c2c;
            color: #ffffff;
            text-decoration: none;
            border-radius: 8px;
            font-size: 16px;
            font-weight: 600;
            box-shadow: 0 4px 12px rgba(0, 0, 0, 0.2);
        }
        .button:hover {
            background: #1a1a1a;
        }
        .link-box {
            background-color: #f8f9fa;
            border: 1px solid #e9ecef;
            border-radius: 6px;
            padding: 15px;
            margin: 20px 0;
            word-break: break-all;
        }
        .link {
            color: #2c2c2c;
            text-decoration: none;
            font-size: 14px;
        }
        .expiry-note {
            background-color: #fff3cd;
            border-left: 4px solid #ffc107;
            padding: 12px 15px;
            margin: 20px 0;
            font-size: 14px;
            color: #856404;
        }
        .security-note {
            background-color: #f8f9fa;
            border-left: 4px solid #6c757d;
            padding: 12px 15px;
            margin: 20px 0;
            font-size: 14px;
            color: #495057;
        }
        .footer {
            text-align: center;
            padding: 30px 40px;
            background-color: #f8f9fa;
            border-top: 1px solid #e9ecef;
        }
        .footer-icon {
            font-size: 32px;
            margin-bottom: 10px;
        }
        .footer p {
            margin: 5px 0;
            font-size: 13px;
            color: #6c757d;
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            {{if .LogoURL}}
                <img src="{{.LogoURL}}" alt="{{.Branding}}" class="logo-img">
            {{else}}
                <div class="logo-icon">📻</div>
            {{end}}
            <h1>{{.Branding}}</h1>
        </div>
        <div class="content">
            <p><strong>Verify Your New Email Address</strong></p>
            <p>You've requested to change your email address for your {{.Branding}} account. To complete this change, please verify your new email address by clicking the button below.</p>
            
            <div class="email-highlight">
                📧 New Email Address: {{.NewEmail}}
            </div>
            
            <div class="button-container">
                <a href="{{.VerificationURL}}" class="button">Verify New Email Address</a>
            </div>
            
            <p style="text-align: center; color: #6c757d; font-size: 14px;">
                Or copy and paste this link into your browser:
            </p>
            <div class="link-box">
                <a href="{{.VerificationURL}}" class="link">{{.VerificationURL}}</a>
            </div>
            
            <div class="expiry-note">
                ⏰ This verification link will expire in 24 hours.
            </div>
            
            <div class="security-note">
                🔒 If you didn't request this email change, please contact support immediately to secure your account.
            </div>
            
            <p style="font-size: 14px; color: #6c757d;">
                Once verified, your account email will be updated and you'll be able to log in with your new email address.
            </p>
        </div>
        <div class="footer">
            {{if .LogoURL}}
                <img src="{{.LogoURL}}" alt="{{.Branding}}" style="max-width: 100px; height: auto; display: block; margin: 0 auto;">
            {{else}}
                <div class="footer-icon">📻</div>
            {{end}}
        </div>
    </div>
</body>
</html>`

	tmpl, err := template.New("emailChange").Parse(htmlTemplate)
	if err != nil {
		// Fallback to simple HTML if template parsing fails
		return fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head><meta charset="UTF-8"></head>
<body>
    <h1>Verify Your New Email Address</h1>
    <p>You've requested to change your email address to: <strong>%s</strong></p>
    <p>Please verify your new email by clicking this link: <a href="%s">%s</a></p>
    <p>If you didn't request this change, please contact support immediately.</p>
</body>
</html>`, newEmail, verificationURL, verificationURL)
	}

	var buf bytes.Buffer
	data := struct {
		Branding        string
		NewEmail        string
		VerificationURL string
		LogoURL         string
	}{
		Branding:        branding,
		NewEmail:        newEmail,
		VerificationURL: verificationURL,
		LogoURL:         logoURL,
	}

	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head><meta charset="UTF-8"></head>
<body>
    <h1>Verify Your New Email Address</h1>
    <p>You've requested to change your email address to: <strong>%s</strong></p>
    <p>Please verify your new email by clicking this link: <a href="%s">%s</a></p>
    <p>If you didn't request this change, please contact support immediately.</p>
</body>
</html>`, newEmail, verificationURL, verificationURL)
	}

	return buf.String()
}

func (es *EmailService) SendVerificationEmail(user *User) error {
	// Check if email service is enabled and configured
	if !es.Controller.Options.EmailServiceEnabled {
		log.Printf("Email service is disabled")
		return fmt.Errorf("email service is disabled")
	}

	// Validate email provider configuration
	if es.Controller.Options.EmailProvider == "" {
		log.Printf("Email provider not configured")
		return fmt.Errorf("email provider not configured")
	}

	provider := strings.ToLower(es.Controller.Options.EmailProvider)
	if provider == "sendgrid" && es.Controller.Options.EmailSendGridAPIKey == "" {
		log.Printf("SendGrid API key not configured")
		return fmt.Errorf("SendGrid API key not configured")
	}
	if provider == "mailgun" && (es.Controller.Options.EmailMailgunAPIKey == "" || es.Controller.Options.EmailMailgunDomain == "") {
		log.Printf("Mailgun not properly configured - missing API key or domain")
		return fmt.Errorf("Mailgun not properly configured")
	}
	if provider == "smtp" && es.Controller.Options.EmailSmtpHost == "" {
		log.Printf("SMTP host not configured")
		return fmt.Errorf("SMTP host not configured")
	}
	if es.Controller.Options.EmailSmtpFromEmail == "" {
		log.Printf("From email address not configured")
		return fmt.Errorf("from email address not configured")
	}

	// Build verification URL - always use HTTPS
	// Send link directly to /?verify=token to avoid redirect
	baseUrl := es.Controller.Options.BaseUrl
	if baseUrl == "" {
		baseUrl = "https://localhost:8080"
	} else {
		// Always ensure HTTPS
		if strings.HasPrefix(baseUrl, "http://") {
			// Replace http:// with https://
			baseUrl = strings.Replace(baseUrl, "http://", "https://", 1)
		} else if !strings.HasPrefix(baseUrl, "https://") {
			// If no scheme, add https://
			baseUrl = "https://" + baseUrl
		}
	}
	verificationLink := baseUrl + "/?verify=" + user.VerificationToken

	// Get branding
	branding := es.Controller.Options.Branding
	if branding == "" {
		branding = "ThinLine Radio"
	}

	// Get from name
	fromName := es.Controller.Options.EmailSmtpFromName
	if fromName == "" {
		fromName = branding
	}

	// Get logo URL (if uploaded by admin)
	var logoURL string
	if es.Controller.Options.EmailLogoFilename != "" {
		// Build full URL to logo
		baseUrl := es.Controller.Options.BaseUrl
		if baseUrl == "" {
			baseUrl = "https://localhost:8080"
		} else {
			// Always ensure HTTPS
			if strings.HasPrefix(baseUrl, "http://") {
				baseUrl = strings.Replace(baseUrl, "http://", "https://", 1)
			} else if !strings.HasPrefix(baseUrl, "https://") {
				baseUrl = "https://" + baseUrl
			}
		}
		logoURL = baseUrl + "/email-logo"
		log.Printf("📧 Using custom logo: %s", logoURL)
	} else {
		log.Printf("📧 Using default icon (no custom logo set)")
	}

	// Get border radius for logo
	borderRadius := es.Controller.Options.EmailLogoBorderRadius
	if borderRadius == "" {
		borderRadius = "0px"
	}

	// Check if user's group has billing enabled
	billingEnabled := false
	if user.UserGroupId > 0 {
		userGroup := es.Controller.UserGroups.Get(user.UserGroupId)
		if userGroup != nil && userGroup.BillingEnabled {
			billingEnabled = true
			log.Printf("📧 User %s is in billing-enabled group %s - will include subscription notice in verification email", user.Email, userGroup.Name)
		}
	}

	// Generate email HTML
	htmlBody := getVerificationEmailHTML(user.Email, verificationLink, branding, logoURL, borderRadius, billingEnabled)

	// Set up email headers
	fromEmail := es.Controller.Options.EmailSmtpFromEmail
	toEmail := user.Email
	subject := fmt.Sprintf("📻 Verify Your Email - %s", branding)

	// Extract domain for HELO
	domain := extractDomainFromEmail(fromEmail)

	log.Printf("📧 Sending verification email to %s with HELO %s...", user.Email, domain)

	// Remove emojis from subject and body
	subject = removeEmojis(subject)
	htmlBody = removeEmojisFromHTML(htmlBody)

	// Send email using provider routing
	if err := es.sendEmail(fromName, fromEmail, toEmail, subject, htmlBody); err != nil {
		return err
	}

	log.Printf("✅ Verification email sent successfully to %s", user.Email)
	return nil
}

// SendSignupVerificationEmail sends a verification CODE (not link) during signup
func (es *EmailService) SendSignupVerificationEmail(email, verificationCode string) error {
	// Check if email service is enabled and configured
	if !es.Controller.Options.EmailServiceEnabled {
		log.Printf("Email service is disabled")
		return fmt.Errorf("email service is disabled")
	}

	// Validate email provider configuration
	if es.Controller.Options.EmailProvider == "" {
		log.Printf("Email provider not configured")
		return fmt.Errorf("email provider not configured")
	}

	provider := strings.ToLower(es.Controller.Options.EmailProvider)
	if provider == "sendgrid" && es.Controller.Options.EmailSendGridAPIKey == "" {
		log.Printf("SendGrid API key not configured")
		return fmt.Errorf("SendGrid API key not configured")
	}
	if provider == "mailgun" && (es.Controller.Options.EmailMailgunAPIKey == "" || es.Controller.Options.EmailMailgunDomain == "") {
		log.Printf("Mailgun not properly configured - missing API key or domain")
		return fmt.Errorf("Mailgun not properly configured")
	}
	if provider == "smtp" && es.Controller.Options.EmailSmtpHost == "" {
		log.Printf("SMTP host not configured")
		return fmt.Errorf("SMTP host not configured")
	}
	if es.Controller.Options.EmailSmtpFromEmail == "" {
		log.Printf("From email address not configured")
		return fmt.Errorf("from email address not configured")
	}

	// Get branding
	branding := es.Controller.Options.Branding
	if branding == "" {
		branding = "ThinLine Radio"
	}

	// Get from name
	fromName := es.Controller.Options.EmailSmtpFromName
	if fromName == "" {
		fromName = branding
	}

	// Get logo URL (if uploaded by admin)
	var logoURL string
	if es.Controller.Options.EmailLogoFilename != "" {
		baseUrl := es.Controller.Options.BaseUrl
		if baseUrl == "" {
			baseUrl = "https://localhost:8080"
		} else {
			if strings.HasPrefix(baseUrl, "http://") {
				baseUrl = strings.Replace(baseUrl, "http://", "https://", 1)
			} else if !strings.HasPrefix(baseUrl, "https://") {
				baseUrl = "https://" + baseUrl
			}
		}
		logoURL = baseUrl + "/email-logo"
	}

	// Get border radius for logo
	borderRadius := es.Controller.Options.EmailLogoBorderRadius
	if borderRadius == "" {
		borderRadius = "0px"
	}

	// Generate email HTML with verification code
	htmlBody := getSignupVerificationEmailHTML(email, verificationCode, branding, logoURL, borderRadius)

	// Set up email headers
	fromEmail := es.Controller.Options.EmailSmtpFromEmail
	toEmail := email
	subject := fmt.Sprintf("📻 Email Verification Code - %s", branding)

	// Extract domain for HELO
	domain := extractDomainFromEmail(fromEmail)

	log.Printf("📧 Sending signup verification email to %s with HELO %s...", email, domain)

	// Remove emojis from subject and body
	subject = removeEmojis(subject)
	htmlBody = removeEmojisFromHTML(htmlBody)

	// Send email using provider routing
	if err := es.sendEmail(fromName, fromEmail, toEmail, subject, htmlBody); err != nil {
		return err
	}

	log.Printf("✅ Signup verification email sent successfully to %s", email)
	return nil
}

// getSignupVerificationEmailHTML generates the HTML content for signup verification code emails
func getSignupVerificationEmailHTML(email, verificationCode, branding, logoURL, borderRadius string) string {
	if branding == "" {
		branding = "ThinLine Radio"
	}

	htmlTemplate := `<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Verify Your Email - {{.Branding}}</title>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif;
            line-height: 1.6;
            color: #333;
            max-width: 600px;
            margin: 0 auto;
            padding: 20px;
            background-color: #f5f5f5;
        }
        .container {
            background-color: #ffffff;
            border-radius: 8px;
            padding: 40px;
            box-shadow: 0 2px 4px rgba(0,0,0,0.1);
        }
        .header {
            text-align: center;
            margin-bottom: 30px;
        }
        .logo {
            max-width: 200px;
            height: auto;
            margin-bottom: 20px;
        }
        h1 {
            color: #2c3e50;
            margin: 0 0 20px 0;
            font-size: 24px;
        }
        .code-box {
            background-color: #f8f9fa;
            border: 2px solid #007bff;
            border-radius: 8px;
            padding: 20px;
            text-align: center;
            margin: 30px 0;
        }
        .code {
            font-size: 32px;
            font-weight: bold;
            color: #007bff;
            letter-spacing: 4px;
            font-family: 'Courier New', monospace;
        }
        .footer {
            margin-top: 30px;
            padding-top: 20px;
            border-top: 1px solid #e0e0e0;
            font-size: 12px;
            color: #777;
            text-align: center;
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            {{if .LogoURL}}
            <img src="{{.LogoURL}}" alt="{{.Branding}}" class="logo" style="border-radius: {{.BorderRadius}};">
            {{end}}
            <h1>Verify Your Email</h1>
        </div>
        
        <p>Hello,</p>
        
        <p>Thank you for signing up for <strong>{{.Branding}}</strong>!</p>
        
        <p>Please enter the following verification code to complete your registration:</p>
        
        <div class="code-box">
            <div class="code">{{.Code}}</div>
        </div>
        
        <p><strong>This code will expire in 15 minutes.</strong></p>
        
        <p>If you didn't request this code, you can safely ignore this email.</p>
        
        <div class="footer">
            <p>This is an automated message from {{.Branding}}.</p>
        </div>
    </div>
</body>
</html>`

	data := map[string]string{
		"Email":        email,
		"Code":         verificationCode,
		"Branding":     branding,
		"LogoURL":      logoURL,
		"BorderRadius": borderRadius,
	}

	html := htmlTemplate
	html = strings.ReplaceAll(html, "{{.Email}}", data["Email"])
	html = strings.ReplaceAll(html, "{{.Code}}", data["Code"])
	html = strings.ReplaceAll(html, "{{.Branding}}", data["Branding"])
	html = strings.ReplaceAll(html, "{{.LogoURL}}", data["LogoURL"])
	html = strings.ReplaceAll(html, "{{.BorderRadius}}", data["BorderRadius"])

	// Handle conditional logo
	if logoURL == "" {
		html = strings.ReplaceAll(html, "{{if .LogoURL}}", "<!--")
		html = strings.ReplaceAll(html, "{{end}}", "-->")
	} else {
		html = strings.ReplaceAll(html, "{{if .LogoURL}}", "")
		html = strings.ReplaceAll(html, "{{end}}", "")
	}

	return html
}

// getPasswordResetEmailHTML generates the HTML content for password reset emails
func getPasswordResetEmailHTML(resetCode, branding, logoURL, borderRadius string) string {
	if branding == "" {
		branding = "ThinLine Radio"
	}

	htmlTemplate := `<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Password Reset - {{.Branding}}</title>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif;
            line-height: 1.6;
            color: #333;
            max-width: 600px;
            margin: 0 auto;
            padding: 20px;
            background-color: #f5f5f5;
        }
        .container {
            background-color: #ffffff;
            border-radius: 12px;
            padding: 0;
            box-shadow: 0 4px 12px rgba(0,0,0,0.1);
            overflow: hidden;
        }
        .header {
            background: #2c2c2c;
            color: white;
            text-align: center;
            padding: 40px 20px;
        }
        .logo-img {
            max-width: 200px;
            height: auto;
            margin: 0 auto 15px auto;
            display: block;
            border-radius: {{.BorderRadius}};
        }
        .logo-icon {
            font-size: 64px;
            margin: 0 auto 15px auto;
            display: block;
            text-align: center;
        }
        h1 {
            color: white;
            margin: 0;
            font-size: 28px;
            font-weight: 600;
        }
        .content {
            padding: 40px;
        }
        .content p {
            margin: 0 0 15px 0;
            color: #555;
            font-size: 16px;
        }
        .content p:first-child {
            font-size: 18px;
            color: #333;
        }
        .code-container {
            text-align: center;
            margin: 30px 0;
        }
        .code-box {
            display: inline-block;
            background: #f8f9fa;
            border: 2px solid #2c2c2c;
            border-radius: 8px;
            padding: 20px 40px;
            font-size: 32px;
            font-weight: 700;
            letter-spacing: 8px;
            color: #2c2c2c;
            font-family: 'Courier New', monospace;
        }
        .expiry-note {
            background-color: #fff3cd;
            border-left: 4px solid #ffc107;
            padding: 12px 15px;
            margin: 20px 0;
            font-size: 14px;
            color: #856404;
        }
        .footer {
            text-align: center;
            padding: 30px 40px;
            background-color: #f8f9fa;
            border-top: 1px solid #e9ecef;
        }
        .footer-icon {
            font-size: 32px;
            margin-bottom: 10px;
        }
        .footer p {
            margin: 5px 0;
            font-size: 13px;
            color: #6c757d;
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            {{if .LogoURL}}
                <img src="{{.LogoURL}}" alt="{{.Branding}}" class="logo-img">
            {{else}}
                <div class="logo-icon">📻</div>
            {{end}}
            <h1>{{.Branding}}</h1>
        </div>
        <div class="content">
            <p><strong>Password Reset Request</strong></p>
            <p>You requested to reset your password. Use the verification code below to reset your password:</p>
            
            <div class="code-container">
                <div class="code-box">{{.ResetCode}}</div>
            </div>
            
            <div class="expiry-note">
                ⏰ This code will expire in 15 minutes.
            </div>
            
            <p style="font-size: 14px; color: #6c757d;">
                If you didn't request a password reset, you can safely ignore this email. Your password will remain unchanged.
            </p>
        </div>
        <div class="footer">
            {{if .LogoURL}}
                <img src="{{.LogoURL}}" alt="{{.Branding}}" style="max-width: 100px; height: auto; display: block; margin: 0 auto; border-radius: {{.BorderRadius}};">
            {{else}}
                <div class="footer-icon">📻</div>
            {{end}}
        </div>
    </div>
</body>
</html>`

	tmpl, err := template.New("email").Parse(htmlTemplate)
	if err != nil {
		// Fallback to simple HTML if template parsing fails
		return fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head><meta charset="UTF-8"></head>
<body>
    <h1>Password Reset</h1>
    <p>Your password reset code is: <strong>%s</strong></p>
    <p>This code will expire in 15 minutes.</p>
</body>
</html>`, resetCode)
	}

	var buf bytes.Buffer
	data := struct {
		Branding     string
		ResetCode    string
		LogoURL      string
		BorderRadius string
	}{
		Branding:     branding,
		ResetCode:    resetCode,
		LogoURL:      logoURL,
		BorderRadius: borderRadius,
	}

	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head><meta charset="UTF-8"></head>
<body>
    <h1>Password Reset</h1>
    <p>Your password reset code is: <strong>%s</strong></p>
    <p>This code will expire in 15 minutes.</p>
</body>
</html>`, resetCode)
	}

	return buf.String()
}

func (es *EmailService) SendPasswordResetEmail(user *User, resetCode string) error {
	// Check if email service is enabled and configured
	if !es.Controller.Options.EmailServiceEnabled {
		log.Printf("Email service is disabled")
		return fmt.Errorf("email service is disabled")
	}

	// Validate email provider configuration
	if es.Controller.Options.EmailProvider == "" {
		log.Printf("Email provider not configured")
		return fmt.Errorf("email provider not configured")
	}

	provider := strings.ToLower(es.Controller.Options.EmailProvider)
	if provider == "sendgrid" && es.Controller.Options.EmailSendGridAPIKey == "" {
		log.Printf("SendGrid API key not configured")
		return fmt.Errorf("SendGrid API key not configured")
	}
	if provider == "mailgun" && (es.Controller.Options.EmailMailgunAPIKey == "" || es.Controller.Options.EmailMailgunDomain == "") {
		log.Printf("Mailgun not properly configured - missing API key or domain")
		return fmt.Errorf("Mailgun not properly configured")
	}
	if provider == "smtp" && es.Controller.Options.EmailSmtpHost == "" {
		log.Printf("SMTP host not configured")
		return fmt.Errorf("SMTP host not configured")
	}
	if es.Controller.Options.EmailSmtpFromEmail == "" {
		log.Printf("From email address not configured")
		return fmt.Errorf("from email address not configured")
	}

	// Get branding
	branding := es.Controller.Options.Branding
	if branding == "" {
		branding = "ThinLine Radio"
	}

	// Get from name
	fromName := es.Controller.Options.EmailSmtpFromName
	if fromName == "" {
		fromName = branding
	}

	// Get logo URL (if uploaded by admin)
	var logoURL string
	if es.Controller.Options.EmailLogoFilename != "" {
		// Build full URL to logo
		baseUrl := es.Controller.Options.BaseUrl
		if baseUrl == "" {
			baseUrl = "https://localhost:8080"
		} else {
			// Always ensure HTTPS
			if strings.HasPrefix(baseUrl, "http://") {
				baseUrl = strings.Replace(baseUrl, "http://", "https://", 1)
			} else if !strings.HasPrefix(baseUrl, "https://") {
				baseUrl = "https://" + baseUrl
			}
		}
		logoURL = baseUrl + "/email-logo"
	}

	// Get border radius for logo
	borderRadius := es.Controller.Options.EmailLogoBorderRadius
	if borderRadius == "" {
		borderRadius = "0px"
	}

	// Generate email HTML
	htmlBody := getPasswordResetEmailHTML(resetCode, branding, logoURL, borderRadius)

	// Set up email headers
	fromEmail := es.Controller.Options.EmailSmtpFromEmail
	toEmail := user.Email
	subject := fmt.Sprintf("📻 Password Reset Code - %s", branding)

	// Extract domain for HELO
	domain := extractDomainFromEmail(fromEmail)

	log.Printf("📧 Sending password reset email to %s with HELO %s...", user.Email, domain)

	// Remove emojis from subject and body
	subject = removeEmojis(subject)
	htmlBody = removeEmojisFromHTML(htmlBody)

	// Send email using provider routing
	if err := es.sendEmail(fromName, fromEmail, toEmail, subject, htmlBody); err != nil {
		return err
	}

	log.Printf("✅ Password reset email sent successfully to %s", user.Email)
	return nil
}

func (es *EmailService) SendEmailChangeVerificationEmail(user *User, verificationCode string) error {
	// Check if email service is enabled and configured
	if !es.Controller.Options.EmailServiceEnabled {
		log.Printf("Email service is disabled")
		return fmt.Errorf("email service is disabled")
	}

	// Validate email provider configuration
	if es.Controller.Options.EmailProvider == "" {
		log.Printf("Email provider not configured")
		return fmt.Errorf("email provider not configured")
	}

	provider := strings.ToLower(es.Controller.Options.EmailProvider)
	if provider == "sendgrid" && es.Controller.Options.EmailSendGridAPIKey == "" {
		log.Printf("SendGrid API key not configured")
		return fmt.Errorf("SendGrid API key not configured")
	}
	if provider == "mailgun" && (es.Controller.Options.EmailMailgunAPIKey == "" || es.Controller.Options.EmailMailgunDomain == "") {
		log.Printf("Mailgun not properly configured - missing API key or domain")
		return fmt.Errorf("Mailgun not properly configured")
	}
	if provider == "smtp" && es.Controller.Options.EmailSmtpHost == "" {
		log.Printf("SMTP host not configured")
		return fmt.Errorf("SMTP host not configured")
	}
	if es.Controller.Options.EmailSmtpFromEmail == "" {
		log.Printf("From email address not configured")
		return fmt.Errorf("from email address not configured")
	}

	// Get branding
	branding := es.Controller.Options.Branding
	if branding == "" {
		branding = "ThinLine Radio"
	}

	// Get from name
	fromName := es.Controller.Options.EmailSmtpFromName
	if fromName == "" {
		fromName = branding
	}

	// Get logo URL (if uploaded by admin)
	var logoURL string
	if es.Controller.Options.EmailLogoFilename != "" {
		baseUrl := es.Controller.Options.BaseUrl
		if baseUrl == "" {
			baseUrl = "https://localhost:8080"
		} else {
			if strings.HasPrefix(baseUrl, "http://") {
				baseUrl = strings.Replace(baseUrl, "http://", "https://", 1)
			} else if !strings.HasPrefix(baseUrl, "https://") {
				baseUrl = "https://" + baseUrl
			}
		}
		logoURL = baseUrl + "/email-logo"
	}

	// Get border radius for logo
	borderRadius := es.Controller.Options.EmailLogoBorderRadius
	if borderRadius == "" {
		borderRadius = "0px"
	}

	// Generate email HTML using email change verification code template
	htmlBody := getEmailChangeVerificationCodeHTML(verificationCode, branding, logoURL)

	// Set up email headers
	fromEmail := es.Controller.Options.EmailSmtpFromEmail
	toEmail := user.Email
	subject := fmt.Sprintf("📻 Email Change Verification Code - %s", branding)

	// Extract domain for HELO
	domain := extractDomainFromEmail(fromEmail)

	log.Printf("📧 Sending email change verification code to %s with HELO %s...", user.Email, domain)

	// Remove emojis from subject and body
	subject = removeEmojis(subject)
	htmlBody = removeEmojisFromHTML(htmlBody)

	// Send email using provider routing
	if err := es.sendEmail(fromName, fromEmail, toEmail, subject, htmlBody); err != nil {
		return err
	}

	log.Printf("✅ Email change verification code sent successfully to %s", user.Email)
	return nil
}

// SendNewEmailVerificationEmail sends a verification email to the new email address during email change
func (es *EmailService) SendNewEmailVerificationEmail(newEmail, verificationToken string) error {
	// Check if email service is enabled and configured
	if !es.Controller.Options.EmailServiceEnabled {
		log.Printf("Email service is disabled")
		return fmt.Errorf("email service is disabled")
	}

	// Validate email provider configuration
	if es.Controller.Options.EmailProvider == "" {
		log.Printf("Email provider not configured")
		return fmt.Errorf("email provider not configured")
	}

	provider := strings.ToLower(es.Controller.Options.EmailProvider)
	if provider == "sendgrid" && es.Controller.Options.EmailSendGridAPIKey == "" {
		log.Printf("SendGrid API key not configured")
		return fmt.Errorf("SendGrid API key not configured")
	}
	if provider == "mailgun" && (es.Controller.Options.EmailMailgunAPIKey == "" || es.Controller.Options.EmailMailgunDomain == "") {
		log.Printf("Mailgun not properly configured - missing API key or domain")
		return fmt.Errorf("Mailgun not properly configured")
	}
	if provider == "smtp" && es.Controller.Options.EmailSmtpHost == "" {
		log.Printf("SMTP host not configured")
		return fmt.Errorf("SMTP host not configured")
	}
	if es.Controller.Options.EmailSmtpFromEmail == "" {
		log.Printf("From email address not configured")
		return fmt.Errorf("from email address not configured")
	}

	// Build verification URL - always use HTTPS
	baseUrl := es.Controller.Options.BaseUrl
	if baseUrl == "" {
		baseUrl = "https://localhost:8080"
	} else {
		// Always ensure HTTPS
		if strings.HasPrefix(baseUrl, "http://") {
			baseUrl = strings.Replace(baseUrl, "http://", "https://", 1)
		} else if !strings.HasPrefix(baseUrl, "https://") {
			baseUrl = "https://" + baseUrl
		}
	}
	verificationLink := baseUrl + "/?verify=" + verificationToken

	// Get branding
	branding := es.Controller.Options.Branding
	if branding == "" {
		branding = "ThinLine Radio"
	}

	// Get from name
	fromName := es.Controller.Options.EmailSmtpFromName
	if fromName == "" {
		fromName = branding
	}

	// Get logo URL (if uploaded by admin)
	var logoURL string
	if es.Controller.Options.EmailLogoFilename != "" {
		baseUrl := es.Controller.Options.BaseUrl
		if baseUrl == "" {
			baseUrl = "https://localhost:8080"
		} else {
			if strings.HasPrefix(baseUrl, "http://") {
				baseUrl = strings.Replace(baseUrl, "http://", "https://", 1)
			} else if !strings.HasPrefix(baseUrl, "https://") {
				baseUrl = "https://" + baseUrl
			}
		}
		logoURL = baseUrl + "/email-logo"
	}

	// Generate email HTML using the new template
	htmlBody := getEmailChangeVerificationHTML(newEmail, verificationLink, branding, logoURL)

	// Set up email headers
	fromEmail := es.Controller.Options.EmailSmtpFromEmail
	toEmail := newEmail
	subject := fmt.Sprintf("📻 Verify Your New Email Address - %s", branding)

	// Extract domain for HELO
	domain := extractDomainFromEmail(fromEmail)

	log.Printf("📧 Sending new email verification to %s with HELO %s...", newEmail, domain)

	// Remove emojis from subject and body
	subject = removeEmojis(subject)
	htmlBody = removeEmojisFromHTML(htmlBody)

	// Send email using provider routing
	if err := es.sendEmail(fromName, fromEmail, toEmail, subject, htmlBody); err != nil {
		return err
	}

	log.Printf("✅ New email verification sent successfully to %s", newEmail)
	return nil
}

// getEmailChangeVerificationCodeHTML generates the HTML content for email change verification code emails
func getEmailChangeVerificationCodeHTML(verificationCode, branding, logoURL string) string {
	if branding == "" {
		branding = "ThinLine Radio"
	}

	htmlTemplate := `<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Email Change Verification Code - {{.Branding}}</title>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif;
            line-height: 1.6;
            color: #333;
            max-width: 600px;
            margin: 0 auto;
            padding: 20px;
            background-color: #f5f5f5;
        }
        .container {
            background-color: #ffffff;
            border-radius: 12px;
            padding: 0;
            box-shadow: 0 4px 12px rgba(0,0,0,0.1);
            overflow: hidden;
        }
        .header {
            background: #2c2c2c;
            color: white;
            text-align: center;
            padding: 40px 20px;
        }
        .logo-img {
            max-width: 200px;
            height: auto;
            margin: 0 auto 15px auto;
            display: block;
        }
        .logo-icon {
            font-size: 64px;
            margin: 0 auto 15px auto;
            display: block;
            text-align: center;
        }
        h1 {
            color: white;
            margin: 0;
            font-size: 28px;
            font-weight: 600;
        }
        .content {
            padding: 40px;
        }
        .content p {
            margin: 0 0 15px 0;
            color: #555;
            font-size: 16px;
        }
        .content p:first-child {
            font-size: 18px;
            color: #333;
        }
        .code-box {
            background-color: #f8f9fa;
            border: 2px solid #2c2c2c;
            border-radius: 8px;
            padding: 20px;
            margin: 30px 0;
            text-align: center;
        }
        .code {
            font-size: 36px;
            font-weight: 700;
            color: #2c2c2c;
            letter-spacing: 8px;
            font-family: 'Courier New', monospace;
        }
        .expiry-note {
            background-color: #fff3cd;
            border-left: 4px solid #ffc107;
            padding: 12px 15px;
            margin: 20px 0;
            font-size: 14px;
            color: #856404;
        }
        .security-note {
            background-color: #f8f9fa;
            border-left: 4px solid #6c757d;
            padding: 12px 15px;
            margin: 20px 0;
            font-size: 14px;
            color: #495057;
        }
        .footer {
            text-align: center;
            padding: 30px 40px;
            background-color: #f8f9fa;
            border-top: 1px solid #e9ecef;
        }
        .footer-icon {
            font-size: 32px;
            margin-bottom: 10px;
        }
        .footer p {
            margin: 5px 0;
            font-size: 13px;
            color: #6c757d;
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            {{if .LogoURL}}
                <img src="{{.LogoURL}}" alt="{{.Branding}}" class="logo-img">
            {{else}}
                <div class="logo-icon">📻</div>
            {{end}}
            <h1>{{.Branding}}</h1>
        </div>
        <div class="content">
            <p><strong>Email Change Verification</strong></p>
            <p>You've requested to change your email address for your {{.Branding}} account. To proceed with the email change, please use the verification code below:</p>
            
            <div class="code-box">
                <div class="code">{{.VerificationCode}}</div>
            </div>
            
            <div class="expiry-note">
                ⏰ This verification code will expire in 15 minutes.
            </div>
            
            <div class="security-note">
                🔒 If you didn't request an email change, please contact support immediately to secure your account. Do not share this code with anyone.
            </div>
            
            <p style="font-size: 14px; color: #6c757d;">
                Enter this code in the email change form to verify your identity and complete the email change.
            </p>
        </div>
        <div class="footer">
            {{if .LogoURL}}
                <img src="{{.LogoURL}}" alt="{{.Branding}}" style="max-width: 100px; height: auto; display: block; margin: 0 auto;">
            {{else}}
                <div class="footer-icon">📻</div>
            {{end}}
        </div>
    </div>
</body>
</html>`

	tmpl, err := template.New("emailChangeVerificationCode").Parse(htmlTemplate)
	if err != nil {
		// Fallback to simple HTML if template parsing fails
		return fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head><meta charset="UTF-8"></head>
<body>
    <h1>Email Change Verification Code</h1>
    <p>Your email change verification code is: <strong>%s</strong></p>
    <p>This code will expire in 15 minutes.</p>
    <p>If you didn't request this change, please contact support immediately.</p>
</body>
</html>`, verificationCode)
	}

	var buf bytes.Buffer
	data := struct {
		Branding         string
		VerificationCode string
		LogoURL          string
	}{
		Branding:         branding,
		VerificationCode: verificationCode,
		LogoURL:          logoURL,
	}

	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head><meta charset="UTF-8"></head>
<body>
    <h1>Email Change Verification Code</h1>
    <p>Your email change verification code is: <strong>%s</strong></p>
    <p>This code will expire in 15 minutes.</p>
    <p>If you didn't request this change, please contact support immediately.</p>
</body>
</html>`, verificationCode)
	}

	return buf.String()
}

// getPasswordChangeVerificationEmailHTML generates the HTML content for password change verification code emails
func getPasswordChangeVerificationEmailHTML(verificationCode, branding, logoURL string) string {
	if branding == "" {
		branding = "ThinLine Radio"
	}

	htmlTemplate := `<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Password Change Verification Code - {{.Branding}}</title>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif;
            line-height: 1.6;
            color: #333;
            max-width: 600px;
            margin: 0 auto;
            padding: 20px;
            background-color: #f5f5f5;
        }
        .container {
            background-color: #ffffff;
            border-radius: 12px;
            padding: 0;
            box-shadow: 0 4px 12px rgba(0,0,0,0.1);
            overflow: hidden;
        }
        .header {
            background: #2c2c2c;
            color: white;
            text-align: center;
            padding: 40px 20px;
        }
        .logo-img {
            max-width: 200px;
            height: auto;
            margin: 0 auto 15px auto;
            display: block;
        }
        .logo-icon {
            font-size: 64px;
            margin: 0 auto 15px auto;
            display: block;
            text-align: center;
        }
        h1 {
            color: white;
            margin: 0;
            font-size: 28px;
            font-weight: 600;
        }
        .content {
            padding: 40px;
        }
        .content p {
            margin: 0 0 15px 0;
            color: #555;
            font-size: 16px;
        }
        .content p:first-child {
            font-size: 18px;
            color: #333;
        }
        .code-box {
            background-color: #f8f9fa;
            border: 2px solid #2c2c2c;
            border-radius: 8px;
            padding: 20px;
            margin: 30px 0;
            text-align: center;
        }
        .code {
            font-size: 36px;
            font-weight: 700;
            color: #2c2c2c;
            letter-spacing: 8px;
            font-family: 'Courier New', monospace;
        }
        .expiry-note {
            background-color: #fff3cd;
            border-left: 4px solid #ffc107;
            padding: 12px 15px;
            margin: 20px 0;
            font-size: 14px;
            color: #856404;
        }
        .security-note {
            background-color: #f8f9fa;
            border-left: 4px solid #6c757d;
            padding: 12px 15px;
            margin: 20px 0;
            font-size: 14px;
            color: #495057;
        }
        .footer {
            text-align: center;
            padding: 30px 40px;
            background-color: #f8f9fa;
            border-top: 1px solid #e9ecef;
        }
        .footer-icon {
            font-size: 32px;
            margin-bottom: 10px;
        }
        .footer p {
            margin: 5px 0;
            font-size: 13px;
            color: #6c757d;
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            {{if .LogoURL}}
                <img src="{{.LogoURL}}" alt="{{.Branding}}" class="logo-img">
            {{else}}
                <div class="logo-icon">📻</div>
            {{end}}
            <h1>{{.Branding}}</h1>
        </div>
        <div class="content">
            <p><strong>Password Change Verification</strong></p>
            <p>You've requested to change your password for your {{.Branding}} account. To proceed with the password change, please use the verification code below:</p>
            
            <div class="code-box">
                <div class="code">{{.VerificationCode}}</div>
            </div>
            
            <div class="expiry-note">
                ⏰ This verification code will expire in 15 minutes.
            </div>
            
            <div class="security-note">
                🔒 If you didn't request a password change, please contact support immediately to secure your account. Do not share this code with anyone.
            </div>
            
            <p style="font-size: 14px; color: #6c757d;">
                Enter this code in the password change form to verify your identity and complete the password change.
            </p>
        </div>
        <div class="footer">
            {{if .LogoURL}}
                <img src="{{.LogoURL}}" alt="{{.Branding}}" style="max-width: 100px; height: auto; display: block; margin: 0 auto;">
            {{else}}
                <div class="footer-icon">📻</div>
            {{end}}
        </div>
    </div>
</body>
</html>`

	tmpl, err := template.New("passwordChange").Parse(htmlTemplate)
	if err != nil {
		// Fallback to simple HTML if template parsing fails
		return fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head><meta charset="UTF-8"></head>
<body>
    <h1>Password Change Verification Code</h1>
    <p>Your password change verification code is: <strong>%s</strong></p>
    <p>This code will expire in 15 minutes.</p>
    <p>If you didn't request this change, please contact support immediately.</p>
</body>
</html>`, verificationCode)
	}

	var buf bytes.Buffer
	data := struct {
		Branding         string
		VerificationCode string
		LogoURL          string
	}{
		Branding:         branding,
		VerificationCode: verificationCode,
		LogoURL:          logoURL,
	}

	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head><meta charset="UTF-8"></head>
<body>
    <h1>Password Change Verification Code</h1>
    <p>Your password change verification code is: <strong>%s</strong></p>
    <p>This code will expire in 15 minutes.</p>
    <p>If you didn't request this change, please contact support immediately.</p>
</body>
</html>`, verificationCode)
	}

	return buf.String()
}

// SendPasswordChangeVerificationEmail sends a verification code email for password changes
func (es *EmailService) SendPasswordChangeVerificationEmail(user *User, verificationCode string) error {
	// Check if email service is enabled and configured
	if !es.Controller.Options.EmailServiceEnabled {
		log.Printf("Email service is disabled")
		return fmt.Errorf("email service is disabled")
	}

	// Validate email provider configuration
	if es.Controller.Options.EmailProvider == "" {
		log.Printf("Email provider not configured")
		return fmt.Errorf("email provider not configured")
	}

	provider := strings.ToLower(es.Controller.Options.EmailProvider)
	if provider == "sendgrid" && es.Controller.Options.EmailSendGridAPIKey == "" {
		log.Printf("SendGrid API key not configured")
		return fmt.Errorf("SendGrid API key not configured")
	}
	if provider == "mailgun" && (es.Controller.Options.EmailMailgunAPIKey == "" || es.Controller.Options.EmailMailgunDomain == "") {
		log.Printf("Mailgun not properly configured - missing API key or domain")
		return fmt.Errorf("Mailgun not properly configured")
	}
	if provider == "smtp" && es.Controller.Options.EmailSmtpHost == "" {
		log.Printf("SMTP host not configured")
		return fmt.Errorf("SMTP host not configured")
	}
	if es.Controller.Options.EmailSmtpFromEmail == "" {
		log.Printf("From email address not configured")
		return fmt.Errorf("from email address not configured")
	}

	// Get branding
	branding := es.Controller.Options.Branding
	if branding == "" {
		branding = "ThinLine Radio"
	}

	// Get from name
	fromName := es.Controller.Options.EmailSmtpFromName
	if fromName == "" {
		fromName = branding
	}

	// Get logo URL (if uploaded by admin)
	var logoURL string
	if es.Controller.Options.EmailLogoFilename != "" {
		baseUrl := es.Controller.Options.BaseUrl
		if baseUrl == "" {
			baseUrl = "https://localhost:8080"
		} else {
			if strings.HasPrefix(baseUrl, "http://") {
				baseUrl = strings.Replace(baseUrl, "http://", "https://", 1)
			} else if !strings.HasPrefix(baseUrl, "https://") {
				baseUrl = "https://" + baseUrl
			}
		}
		logoURL = baseUrl + "/email-logo"
	}

	// Generate email HTML using the password change verification template
	htmlBody := getPasswordChangeVerificationEmailHTML(verificationCode, branding, logoURL)

	// Set up email headers
	fromEmail := es.Controller.Options.EmailSmtpFromEmail
	toEmail := user.Email
	subject := fmt.Sprintf("📻 Password Change Verification Code - %s", branding)

	// Extract domain for HELO
	domain := extractDomainFromEmail(fromEmail)

	log.Printf("📧 Sending password change verification code to %s with HELO %s...", user.Email, domain)

	// Remove emojis from subject and body
	subject = removeEmojis(subject)
	htmlBody = removeEmojisFromHTML(htmlBody)

	// Send email using provider routing
	if err := es.sendEmail(fromName, fromEmail, toEmail, subject, htmlBody); err != nil {
		return err
	}

	log.Printf("✅ Password change verification code sent successfully to %s", user.Email)
	return nil
}

// SendInvitationEmail sends an invitation email to a user
func (es *EmailService) SendInvitationEmail(email, code, invitationLink, groupName, branding string) error {
	// Check if email service is enabled and configured
	if !es.Controller.Options.EmailServiceEnabled {
		log.Printf("Email service is disabled")
		return fmt.Errorf("email service is disabled")
	}

	// Validate email provider configuration
	if es.Controller.Options.EmailProvider == "" {
		log.Printf("Email provider not configured")
		return fmt.Errorf("email provider not configured")
	}

	provider := strings.ToLower(es.Controller.Options.EmailProvider)
	if provider == "sendgrid" && es.Controller.Options.EmailSendGridAPIKey == "" {
		log.Printf("SendGrid API key not configured")
		return fmt.Errorf("SendGrid API key not configured")
	}
	if provider == "mailgun" && (es.Controller.Options.EmailMailgunAPIKey == "" || es.Controller.Options.EmailMailgunDomain == "") {
		log.Printf("Mailgun not properly configured - missing API key or domain")
		return fmt.Errorf("Mailgun not properly configured")
	}
	if provider == "smtp" && es.Controller.Options.EmailSmtpHost == "" {
		log.Printf("SMTP host not configured")
		return fmt.Errorf("SMTP host not configured")
	}
	if es.Controller.Options.EmailSmtpFromEmail == "" {
		log.Printf("From email address not configured")
		return fmt.Errorf("from email address not configured")
	}

	// Get logo URL (if uploaded by admin)
	var logoURL string
	if es.Controller.Options.EmailLogoFilename != "" {
		baseUrl := es.Controller.Options.BaseUrl
		if baseUrl == "" {
			baseUrl = "https://localhost:8080"
		} else {
			if strings.HasPrefix(baseUrl, "http://") {
				baseUrl = strings.Replace(baseUrl, "http://", "https://", 1)
			} else if !strings.HasPrefix(baseUrl, "https://") {
				baseUrl = "https://" + baseUrl
			}
		}
		logoURL = baseUrl + "/email-logo"
	}

	// Get border radius for logo
	borderRadius := es.Controller.Options.EmailLogoBorderRadius
	if borderRadius == "" {
		borderRadius = "0px"
	}

	// Generate email HTML
	htmlBody := getInvitationEmailHTML(email, code, invitationLink, groupName, branding, logoURL, borderRadius)

	// Set up email headers
	fromName := es.Controller.Options.EmailSmtpFromName
	if fromName == "" {
		fromName = branding
	}
	fromEmail := es.Controller.Options.EmailSmtpFromEmail
	toEmail := email
	subject := fmt.Sprintf("📻 You're Invited to Join %s - %s", groupName, branding)

	// Extract domain for HELO
	domain := extractDomainFromEmail(fromEmail)

	log.Printf("📧 Sending invitation email to %s with HELO %s...", email, domain)

	// Remove emojis from subject and body
	subject = removeEmojis(subject)
	htmlBody = removeEmojisFromHTML(htmlBody)

	// Send email using provider routing
	if err := es.sendEmail(fromName, fromEmail, toEmail, subject, htmlBody); err != nil {
		return err
	}

	log.Printf("✅ Invitation email sent successfully to %s", email)
	return nil
}

// getInvitationEmailHTML generates the HTML content for invitation emails
func getInvitationEmailHTML(email, code, invitationLink, groupName, branding, logoURL, borderRadius string) string {
	if branding == "" {
		branding = "ThinLine Radio"
	}

	htmlTemplate := `<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>You're Invited - {{.Branding}}</title>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif;
            line-height: 1.6;
            color: #333;
            max-width: 600px;
            margin: 0 auto;
            padding: 20px;
            background-color: #f5f5f5;
        }
        .container {
            background-color: #ffffff;
            border-radius: 12px;
            padding: 0;
            box-shadow: 0 4px 12px rgba(0,0,0,0.1);
            overflow: hidden;
        }
        .header {
            background: #2c2c2c;
            color: white;
            text-align: center;
            padding: 30px 20px;
        }
        .logo {
            max-width: 80px;
            height: auto;
            margin-bottom: 15px;
            border-radius: {{.BorderRadius}};
        }
        .content {
            padding: 30px;
        }
        .button {
            display: inline-block;
            padding: 14px 28px;
            background-color: #4CAF50;
            color: white;
            text-decoration: none;
            border-radius: 6px;
            font-weight: 600;
            margin: 20px 0;
            text-align: center;
        }
        .button:hover {
            background-color: #45a049;
        }
        .code-box {
            background-color: #f5f5f5;
            border: 2px dashed #ccc;
            border-radius: 6px;
            padding: 15px;
            text-align: center;
            font-family: monospace;
            font-size: 18px;
            font-weight: bold;
            color: #2c2c2c;
            margin: 20px 0;
        }
        .footer {
            text-align: center;
            padding: 20px;
            color: #666;
            font-size: 12px;
            border-top: 1px solid #eee;
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            {{if .LogoURL}}<img src="{{.LogoURL}}" alt="{{.Branding}}" class="logo">{{end}}
            <h1>You're Invited! 🎉</h1>
        </div>
        <div class="content">
            <p>Hello!</p>
            <p>You've been invited to join <strong>{{.GroupName}}</strong> on {{.Branding}}.</p>
            <p>Click the button below to accept your invitation and create your account:</p>
            <div style="text-align: center;">
                <a href="{{.InvitationLink}}" class="button">Accept Invitation</a>
            </div>
            <p>Or use this invitation code when registering:</p>
            <div class="code-box">{{.Code}}</div>
            <p>This invitation will expire in 7 days.</p>
            <p>If you didn't expect this invitation, you can safely ignore this email.</p>
        </div>
        <div class="footer">
            <p>{{.Branding}}</p>
            <p>This email was sent to {{.Email}}</p>
        </div>
    </div>
</body>
</html>`

	tmpl, err := template.New("invitation").Parse(htmlTemplate)
	if err != nil {
		log.Printf("Error parsing invitation email template: %v", err)
		return fmt.Sprintf("<html><body><h1>You're Invited!</h1><p>You've been invited to join %s. Use code: %s</p><p><a href=\"%s\">Accept Invitation</a></p></body></html>", groupName, code, invitationLink)
	}

	var buf bytes.Buffer
	data := struct {
		Email          string
		Code           string
		InvitationLink string
		GroupName      string
		Branding       string
		LogoURL        string
		BorderRadius   string
	}{
		Email:          email,
		Code:           code,
		InvitationLink: invitationLink,
		GroupName:      groupName,
		Branding:       branding,
		LogoURL:        logoURL,
		BorderRadius:   borderRadius,
	}

	if err := tmpl.Execute(&buf, data); err != nil {
		log.Printf("Error executing invitation email template: %v", err)
		return fmt.Sprintf("<html><body><h1>You're Invited!</h1><p>You've been invited to join %s. Use code: %s</p><p><a href=\"%s\">Accept Invitation</a></p></body></html>", groupName, code, invitationLink)
	}

	return buf.String()
}

// SendUserGroupChangeEmail sends an email to a user when they are moved to a different group
func (es *EmailService) SendUserGroupChangeEmail(user *User, newGroup *UserGroup, oldGroup *UserGroup) error {
	if !es.Controller.Options.EmailServiceEnabled {
		return fmt.Errorf("email service is disabled")
	}

	// Validate email provider configuration
	if es.Controller.Options.EmailProvider == "" {
		log.Printf("Email provider not configured")
		return fmt.Errorf("email provider not configured")
	}

	provider := strings.ToLower(es.Controller.Options.EmailProvider)
	if provider == "sendgrid" && es.Controller.Options.EmailSendGridAPIKey == "" {
		log.Printf("SendGrid API key not configured")
		return fmt.Errorf("SendGrid API key not configured")
	}
	if provider == "mailgun" && (es.Controller.Options.EmailMailgunAPIKey == "" || es.Controller.Options.EmailMailgunDomain == "") {
		log.Printf("Mailgun not properly configured - missing API key or domain")
		return fmt.Errorf("Mailgun not properly configured")
	}
	if provider == "smtp" && es.Controller.Options.EmailSmtpHost == "" {
		log.Printf("SMTP host not configured")
		return fmt.Errorf("SMTP host not configured")
	}
	if es.Controller.Options.EmailSmtpFromEmail == "" {
		log.Printf("From email address not configured")
		return fmt.Errorf("from email address not configured")
	}

	branding := es.Controller.Options.Branding
	if branding == "" {
		branding = "ThinLine Radio"
	}

	fromName := es.Controller.Options.EmailSmtpFromName
	if fromName == "" {
		fromName = branding
	}

	var logoURL string
	if es.Controller.Options.EmailLogoFilename != "" {
		baseUrl := es.Controller.Options.BaseUrl
		if baseUrl == "" {
			baseUrl = "https://localhost:8080"
		} else {
			if strings.HasPrefix(baseUrl, "http://") {
				baseUrl = strings.Replace(baseUrl, "http://", "https://", 1)
			} else if !strings.HasPrefix(baseUrl, "https://") {
				baseUrl = "https://" + baseUrl
			}
		}
		logoURL = baseUrl + "/email-logo"
	}

	borderRadius := es.Controller.Options.EmailLogoBorderRadius
	if borderRadius == "" {
		borderRadius = "0px"
	}

	// Determine if billing is required
	billingRequired := newGroup != nil && newGroup.BillingEnabled
	// Grace period message is not shown in transfer emails
	gracePeriodApplied := false

	// Generate email HTML
	htmlBody := getUserGroupChangeEmailHTML(user, newGroup, oldGroup, branding, logoURL, borderRadius, billingRequired, gracePeriodApplied)

	fromEmail := es.Controller.Options.EmailSmtpFromEmail
	toEmail := user.Email
	subject := fmt.Sprintf("📻 Account Group Changed - %s", branding)

	// Extract domain for HELO
	domain := extractDomainFromEmail(fromEmail)

	log.Printf("📧 Sending user group change email to %s with HELO %s...", user.Email, domain)

	// Remove emojis from subject and body
	subject = removeEmojis(subject)
	htmlBody = removeEmojisFromHTML(htmlBody)

	// Send email using provider routing
	if err := es.sendEmail(fromName, fromEmail, toEmail, subject, htmlBody); err != nil {
		return err
	}

	log.Printf("✅ User group change email sent successfully to %s", user.Email)
	return nil
}

// SendUserMovedFromGroupEmail sends an email to group admin when a user is moved from their group
func (es *EmailService) SendUserMovedFromGroupEmail(admin *User, movedUser *User, oldGroup *UserGroup, newGroup *UserGroup) error {
	if !es.Controller.Options.EmailServiceEnabled {
		return fmt.Errorf("email service is disabled")
	}

	// Validate email provider configuration
	if es.Controller.Options.EmailProvider == "" {
		log.Printf("Email provider not configured")
		return fmt.Errorf("email provider not configured")
	}

	provider := strings.ToLower(es.Controller.Options.EmailProvider)
	if provider == "sendgrid" && es.Controller.Options.EmailSendGridAPIKey == "" {
		log.Printf("SendGrid API key not configured")
		return fmt.Errorf("SendGrid API key not configured")
	}
	if provider == "mailgun" && (es.Controller.Options.EmailMailgunAPIKey == "" || es.Controller.Options.EmailMailgunDomain == "") {
		log.Printf("Mailgun not properly configured - missing API key or domain")
		return fmt.Errorf("Mailgun not properly configured")
	}
	if provider == "smtp" && es.Controller.Options.EmailSmtpHost == "" {
		log.Printf("SMTP host not configured")
		return fmt.Errorf("SMTP host not configured")
	}
	if es.Controller.Options.EmailSmtpFromEmail == "" {
		log.Printf("From email address not configured")
		return fmt.Errorf("from email address not configured")
	}

	branding := es.Controller.Options.Branding
	if branding == "" {
		branding = "ThinLine Radio"
	}

	fromName := es.Controller.Options.EmailSmtpFromName
	if fromName == "" {
		fromName = branding
	}

	var logoURL string
	if es.Controller.Options.EmailLogoFilename != "" {
		baseUrl := es.Controller.Options.BaseUrl
		if baseUrl == "" {
			baseUrl = "https://localhost:8080"
		} else {
			if strings.HasPrefix(baseUrl, "http://") {
				baseUrl = strings.Replace(baseUrl, "http://", "https://", 1)
			} else if !strings.HasPrefix(baseUrl, "https://") {
				baseUrl = "https://" + baseUrl
			}
		}
		logoURL = baseUrl + "/email-logo"
	}

	borderRadius := es.Controller.Options.EmailLogoBorderRadius
	if borderRadius == "" {
		borderRadius = "0px"
	}

	// Generate email HTML
	htmlBody := getUserMovedFromGroupEmailHTML(admin, movedUser, oldGroup, newGroup, branding, logoURL, borderRadius)

	fromEmail := es.Controller.Options.EmailSmtpFromEmail
	toEmail := admin.Email
	subject := fmt.Sprintf("📻 User Moved from Your Group - %s", branding)

	// Extract domain for HELO
	domain := extractDomainFromEmail(fromEmail)

	log.Printf("📧 Sending user moved notification email to %s with HELO %s...", admin.Email, domain)

	// Remove emojis from subject and body
	subject = removeEmojis(subject)
	htmlBody = removeEmojisFromHTML(htmlBody)

	// Send email using provider routing
	if err := es.sendEmail(fromName, fromEmail, toEmail, subject, htmlBody); err != nil {
		return err
	}

	log.Printf("✅ User moved notification email sent successfully to %s", admin.Email)
	return nil
}

// getUserGroupChangeEmailHTML generates HTML for user group change notification
func getUserGroupChangeEmailHTML(user *User, newGroup *UserGroup, oldGroup *UserGroup, branding, logoURL, borderRadius string, billingRequired bool, gracePeriodApplied bool) string {
	if branding == "" {
		branding = "ThinLine Radio"
	}

	oldGroupName := "your previous group"
	if oldGroup != nil {
		oldGroupName = oldGroup.Name
	}

	newGroupName := "your new group"
	if newGroup != nil {
		newGroupName = newGroup.Name
	}

	htmlTemplate := `<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Account Group Changed - {{.Branding}}</title>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif;
            line-height: 1.6;
            color: #333;
            max-width: 600px;
            margin: 0 auto;
            padding: 20px;
            background-color: #f5f5f5;
        }
        .container {
            background-color: #ffffff;
            border-radius: 12px;
            padding: 0;
            box-shadow: 0 4px 12px rgba(0,0,0,0.1);
            overflow: hidden;
        }
        .header {
            background: #2c2c2c;
            color: white;
            text-align: center;
            padding: 40px 20px;
        }
        .logo-img {
            max-width: 200px;
            height: auto;
            margin: 0 auto 15px auto;
            display: block;
            border-radius: {{.BorderRadius}};
        }
        .logo-icon {
            font-size: 64px;
            margin: 0 auto 15px auto;
            display: block;
            text-align: center;
        }
        h1 {
            color: white;
            margin: 0;
            font-size: 28px;
            font-weight: 600;
        }
        .content {
            padding: 40px;
        }
        .content p {
            margin: 0 0 15px 0;
            color: #555;
            font-size: 16px;
        }
        .info-box {
            background-color: #e7f3ff;
            border-left: 4px solid #2196F3;
            padding: 15px;
            margin: 20px 0;
            font-size: 16px;
        }
        .footer {
            text-align: center;
            padding: 30px 40px;
            background-color: #f8f9fa;
            border-top: 1px solid #e9ecef;
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            {{if .LogoURL}}
                <img src="{{.LogoURL}}" alt="{{.Branding}}" class="logo-img">
            {{else}}
                <div class="logo-icon">📻</div>
            {{end}}
            <h1>{{.Branding}}</h1>
        </div>
        <div class="content">
            <p><strong>Your Account Group Has Changed</strong></p>
            <p>Hello {{.UserName}},</p>
            <p>Your account has been moved from <strong>{{.OldGroupName}}</strong> to <strong>{{.NewGroupName}}</strong>.</p>
            <div class="info-box">
                <p><strong>New Group:</strong> {{.NewGroupName}}</p>
            </div>
            {{if .BillingRequired}}
            <div class="info-box" style="background-color: #fff3cd; border-left-color: #ffc107;">
                <p><strong>⚠️ Subscription Required</strong></p>
                {{if .GracePeriodApplied}}
                <p>A <strong>15-day account grace period</strong> has been applied to your account. You will need to add a subscription to your account to continue accessing the service after this period.</p>
                {{else}}
                <p>You will need to add a subscription to your account to continue accessing the service.</p>
                {{end}}
                <p><strong>To manage your subscription:</strong></p>
                <ol>
                    <li>Log in to your account</li>
                    <li>Go to <strong>Settings</strong></li>
                    <li>Click on <strong>Manage Subscription</strong></li>
                </ol>
            </div>
            {{end}}
            <p>If you have any questions about this change, please contact your group administrator.</p>
        </div>
        <div class="footer">
            {{if .LogoURL}}
                <img src="{{.LogoURL}}" alt="{{.Branding}}" style="max-width: 100px; height: auto; display: block; margin: 0 auto; border-radius: {{.BorderRadius}};">
            {{else}}
                <div style="font-size: 32px; margin-bottom: 10px;">📻</div>
            {{end}}
            <p>{{.Branding}}</p>
        </div>
    </div>
</body>
</html>`

	tmpl, err := template.New("userGroupChange").Parse(htmlTemplate)
	if err != nil {
		return fmt.Sprintf("<html><body><h1>Account Group Changed</h1><p>Your account has been moved from %s to %s.</p></body></html>", oldGroupName, newGroupName)
	}

	var buf bytes.Buffer
	userName := user.FirstName
	if userName == "" {
		userName = extractNameFromEmail(user.Email)
	}
	data := struct {
		Branding           string
		LogoURL            string
		BorderRadius       string
		UserName           string
		OldGroupName       string
		NewGroupName       string
		BillingRequired    bool
		GracePeriodApplied bool
	}{
		Branding:           branding,
		LogoURL:            logoURL,
		BorderRadius:       borderRadius,
		UserName:           userName,
		OldGroupName:       oldGroupName,
		NewGroupName:       newGroupName,
		BillingRequired:    billingRequired,
		GracePeriodApplied: gracePeriodApplied,
	}

	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Sprintf("<html><body><h1>Account Group Changed</h1><p>Your account has been moved to %s.</p></body></html>", newGroupName)
	}

	return buf.String()
}

// getUserMovedFromGroupEmailHTML generates HTML for group admin notification when user is moved
func getUserMovedFromGroupEmailHTML(admin *User, movedUser *User, oldGroup *UserGroup, newGroup *UserGroup, branding, logoURL, borderRadius string) string {
	if branding == "" {
		branding = "ThinLine Radio"
	}

	oldGroupName := "your group"
	if oldGroup != nil {
		oldGroupName = oldGroup.Name
	}

	newGroupName := "another group"
	if newGroup != nil {
		newGroupName = newGroup.Name
	}

	htmlTemplate := `<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>User Moved from Group - {{.Branding}}</title>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif;
            line-height: 1.6;
            color: #333;
            max-width: 600px;
            margin: 0 auto;
            padding: 20px;
            background-color: #f5f5f5;
        }
        .container {
            background-color: #ffffff;
            border-radius: 12px;
            padding: 0;
            box-shadow: 0 4px 12px rgba(0,0,0,0.1);
            overflow: hidden;
        }
        .header {
            background: #2c2c2c;
            color: white;
            text-align: center;
            padding: 40px 20px;
        }
        .logo-img {
            max-width: 200px;
            height: auto;
            margin: 0 auto 15px auto;
            display: block;
            border-radius: {{.BorderRadius}};
        }
        .logo-icon {
            font-size: 64px;
            margin: 0 auto 15px auto;
            display: block;
            text-align: center;
        }
        h1 {
            color: white;
            margin: 0;
            font-size: 28px;
            font-weight: 600;
        }
        .content {
            padding: 40px;
        }
        .content p {
            margin: 0 0 15px 0;
            color: #555;
            font-size: 16px;
        }
        .info-box {
            background-color: #fff3cd;
            border-left: 4px solid #ffc107;
            padding: 15px;
            margin: 20px 0;
            font-size: 16px;
        }
        .footer {
            text-align: center;
            padding: 30px 40px;
            background-color: #f8f9fa;
            border-top: 1px solid #e9ecef;
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            {{if .LogoURL}}
                <img src="{{.LogoURL}}" alt="{{.Branding}}" class="logo-img">
            {{else}}
                <div class="logo-icon">📻</div>
            {{end}}
            <h1>{{.Branding}}</h1>
        </div>
        <div class="content">
            <p><strong>User Moved from Your Group</strong></p>
            <p>Hello,</p>
            <p>The user <strong>{{.MovedUserEmail}}</strong> ({{.MovedUserName}}) has been moved from <strong>{{.OldGroupName}}</strong> to <strong>{{.NewGroupName}}</strong>.</p>
            <div class="info-box">
                <p><strong>User:</strong> {{.MovedUserEmail}}<br>
                <strong>Moved from:</strong> {{.OldGroupName}}<br>
                <strong>Moved to:</strong> {{.NewGroupName}}</p>
            </div>
            <p>This change was made by another group administrator and did not require your approval.</p>
        </div>
        <div class="footer">
            {{if .LogoURL}}
                <img src="{{.LogoURL}}" alt="{{.Branding}}" style="max-width: 100px; height: auto; display: block; margin: 0 auto; border-radius: {{.BorderRadius}};">
            {{else}}
                <div style="font-size: 32px; margin-bottom: 10px;">📻</div>
            {{end}}
            <p>{{.Branding}}</p>
        </div>
    </div>
</body>
</html>`

	tmpl, err := template.New("userMovedFromGroup").Parse(htmlTemplate)
	if err != nil {
		return fmt.Sprintf("<html><body><h1>User Moved</h1><p>User %s has been moved to %s.</p></body></html>", movedUser.Email, newGroupName)
	}

	var buf bytes.Buffer
	movedUserName := movedUser.FirstName + " " + movedUser.LastName
	if strings.TrimSpace(movedUserName) == "" {
		movedUserName = movedUser.Email
	}
	data := struct {
		Branding       string
		LogoURL        string
		BorderRadius   string
		MovedUserEmail string
		MovedUserName  string
		OldGroupName   string
		NewGroupName   string
	}{
		Branding:       branding,
		LogoURL:        logoURL,
		BorderRadius:   borderRadius,
		MovedUserEmail: movedUser.Email,
		MovedUserName:  movedUserName,
		OldGroupName:   oldGroupName,
		NewGroupName:   newGroupName,
	}

	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Sprintf("<html><body><h1>User Moved</h1><p>User %s has been moved to %s.</p></body></html>", movedUser.Email, newGroupName)
	}

	return buf.String()
}

// SendTransferRequestEmail sends an email to group admin when a transfer request is created
func (es *EmailService) SendTransferRequestEmail(admin *User, transferReq *TransferRequest, targetUser *User, fromGroup *UserGroup, toGroup *UserGroup, approvalToken string) error {
	if !es.Controller.Options.EmailServiceEnabled {
		return fmt.Errorf("email service is disabled")
	}

	// Validate email provider configuration
	if es.Controller.Options.EmailProvider == "" {
		log.Printf("Email provider not configured")
		return fmt.Errorf("email provider not configured")
	}

	provider := strings.ToLower(es.Controller.Options.EmailProvider)
	if provider == "sendgrid" && es.Controller.Options.EmailSendGridAPIKey == "" {
		log.Printf("SendGrid API key not configured")
		return fmt.Errorf("SendGrid API key not configured")
	}
	if provider == "mailgun" && (es.Controller.Options.EmailMailgunAPIKey == "" || es.Controller.Options.EmailMailgunDomain == "") {
		log.Printf("Mailgun not properly configured - missing API key or domain")
		return fmt.Errorf("Mailgun not properly configured")
	}
	if provider == "smtp" && es.Controller.Options.EmailSmtpHost == "" {
		log.Printf("SMTP host not configured")
		return fmt.Errorf("SMTP host not configured")
	}
	if es.Controller.Options.EmailSmtpFromEmail == "" {
		log.Printf("From email address not configured")
		return fmt.Errorf("from email address not configured")
	}

	branding := es.Controller.Options.Branding
	if branding == "" {
		branding = "ThinLine Radio"
	}

	fromName := es.Controller.Options.EmailSmtpFromName
	if fromName == "" {
		fromName = branding
	}

	var logoURL string
	if es.Controller.Options.EmailLogoFilename != "" {
		baseUrl := es.Controller.Options.BaseUrl
		if baseUrl == "" {
			baseUrl = "https://localhost:8080"
		} else {
			if strings.HasPrefix(baseUrl, "http://") {
				baseUrl = strings.Replace(baseUrl, "http://", "https://", 1)
			} else if !strings.HasPrefix(baseUrl, "https://") {
				baseUrl = "https://" + baseUrl
			}
		}
		logoURL = baseUrl + "/email-logo"
	}

	borderRadius := es.Controller.Options.EmailLogoBorderRadius
	if borderRadius == "" {
		borderRadius = "0px"
	}

	// Generate approval link
	baseUrl := es.Controller.Options.BaseUrl
	if baseUrl == "" {
		baseUrl = "https://localhost:8080"
	} else {
		if strings.HasPrefix(baseUrl, "http://") {
			baseUrl = strings.Replace(baseUrl, "http://", "https://", 1)
		} else if !strings.HasPrefix(baseUrl, "https://") {
			baseUrl = "https://" + baseUrl
		}
	}
	// Remove trailing slash if present
	baseUrl = strings.TrimSuffix(baseUrl, "/")
	// Use root-level path to avoid Angular service worker intercepting
	approvalLink := fmt.Sprintf("%s/approve-transfer?requestId=%d&token=%s", baseUrl, transferReq.Id, approvalToken)
	log.Printf("DEBUG: Generated approval link: %s", approvalLink)

	// Generate email HTML
	htmlBody := getTransferRequestEmailHTML(admin, transferReq, targetUser, fromGroup, toGroup, approvalLink, branding, logoURL, borderRadius)

	fromEmail := es.Controller.Options.EmailSmtpFromEmail
	toEmail := admin.Email
	subject := fmt.Sprintf("📻 Transfer Request Pending - %s", branding)

	// Extract domain for HELO
	domain := extractDomainFromEmail(fromEmail)

	log.Printf("📧 Sending transfer request email to %s with HELO %s...", admin.Email, domain)

	// Remove emojis from subject and body
	subject = removeEmojis(subject)
	htmlBody = removeEmojisFromHTML(htmlBody)

	// Send email using provider routing
	if err := es.sendEmail(fromName, fromEmail, toEmail, subject, htmlBody); err != nil {
		return err
	}

	log.Printf("✅ Transfer request email sent successfully to %s", admin.Email)
	return nil
}

// getTransferRequestEmailHTML generates HTML for transfer request notification
func getTransferRequestEmailHTML(admin *User, transferReq *TransferRequest, targetUser *User, fromGroup *UserGroup, toGroup *UserGroup, approvalLink string, branding, logoURL, borderRadius string) string {
	if branding == "" {
		branding = "ThinLine Radio"
	}

	userName := targetUser.FirstName + " " + targetUser.LastName
	if strings.TrimSpace(userName) == "" {
		userName = targetUser.Email
	}

	htmlTemplate := `<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Transfer Request - {{.Branding}}</title>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif;
            line-height: 1.6;
            color: #333;
            max-width: 600px;
            margin: 0 auto;
            padding: 20px;
            background-color: #f5f5f5;
        }
        .container {
            background-color: #ffffff;
            border-radius: 12px;
            padding: 0;
            box-shadow: 0 4px 12px rgba(0,0,0,0.1);
            overflow: hidden;
        }
        .header {
            background: #2c2c2c;
            color: white;
            text-align: center;
            padding: 40px 20px;
        }
        .logo-img {
            max-width: 200px;
            height: auto;
            margin: 0 auto 15px auto;
            display: block;
            border-radius: {{.BorderRadius}};
        }
        .logo-icon {
            font-size: 64px;
            margin: 0 auto 15px auto;
            display: block;
            text-align: center;
        }
        h1 {
            color: white;
            margin: 0;
            font-size: 28px;
            font-weight: 600;
        }
        .content {
            padding: 40px;
        }
        .content p {
            margin: 0 0 15px 0;
            color: #555;
            font-size: 16px;
        }
        .button-container {
            text-align: center;
            margin: 30px 0;
        }
        .button {
            display: inline-block;
            padding: 16px 40px;
            background: #4CAF50;
            color: #ffffff;
            text-decoration: none;
            border-radius: 8px;
            font-size: 16px;
            font-weight: 600;
            box-shadow: 0 4px 12px rgba(0, 0, 0, 0.2);
            margin: 5px;
        }
        .button.reject {
            background: #f44336;
        }
        .button:hover {
            opacity: 0.9;
        }
        .info-box {
            background-color: #e7f3ff;
            border-left: 4px solid #2196F3;
            padding: 15px;
            margin: 20px 0;
            font-size: 16px;
        }
        .footer {
            text-align: center;
            padding: 30px 40px;
            background-color: #f8f9fa;
            border-top: 1px solid #e9ecef;
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            {{if .LogoURL}}
                <img src="{{.LogoURL}}" alt="{{.Branding}}" class="logo-img">
            {{else}}
                <div class="logo-icon">📻</div>
            {{end}}
            <h1>{{.Branding}}</h1>
        </div>
        <div class="content">
            <p><strong>Transfer Request Pending</strong></p>
            <p>Hello,</p>
            <p>A user transfer request has been submitted to move a user to your group.</p>
            <div class="info-box">
                <p><strong>User:</strong> {{.UserName}} ({{.UserEmail}})<br>
                <strong>From Group:</strong> {{.FromGroupName}}<br>
                <strong>To Group:</strong> {{.ToGroupName}}</p>
            </div>
            <p>Click the button below to approve this transfer request:</p>
            <div class="button-container">
                <a href="{{.ApprovalLink}}" class="button">Approve Transfer</a>
            </div>
            <p style="text-align: center; color: #6c757d; font-size: 14px;">
                Or copy and paste this link into your browser:
            </p>
            <p style="text-align: center; color: #6c757d; font-size: 12px; word-break: break-all;">
                {{.ApprovalLink}}
            </p>
        </div>
        <div class="footer">
            {{if .LogoURL}}
                <img src="{{.LogoURL}}" alt="{{.Branding}}" style="max-width: 100px; height: auto; display: block; margin: 0 auto; border-radius: {{.BorderRadius}};">
            {{else}}
                <div style="font-size: 32px; margin-bottom: 10px;">📻</div>
            {{end}}
            <p>{{.Branding}}</p>
        </div>
    </div>
</body>
</html>`

	tmpl, err := template.New("transferRequest").Parse(htmlTemplate)
	if err != nil {
		return fmt.Sprintf("<html><body><h1>Transfer Request</h1><p>User %s requests to be transferred to %s. <a href=\"%s\">Approve</a></p></body></html>", targetUser.Email, toGroup.Name, approvalLink)
	}

	var buf bytes.Buffer
	data := struct {
		Branding      string
		LogoURL       string
		BorderRadius  string
		UserName      string
		UserEmail     string
		FromGroupName string
		ToGroupName   string
		ApprovalLink  string
	}{
		Branding:      branding,
		LogoURL:       logoURL,
		BorderRadius:  borderRadius,
		UserName:      userName,
		UserEmail:     targetUser.Email,
		FromGroupName: fromGroup.Name,
		ToGroupName:   toGroup.Name,
		ApprovalLink:  approvalLink,
	}

	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Sprintf("<html><body><h1>Transfer Request</h1><p>User %s requests to be transferred to %s. <a href=\"%s\">Approve</a></p></body></html>", targetUser.Email, toGroup.Name, approvalLink)
	}

	return buf.String()
}

// SendTestEmail sends a test email to verify email provider configuration
func (es *EmailService) SendTestEmail(toEmail string) error {
	// Check if email service is enabled
	if !es.Controller.Options.EmailServiceEnabled {
		return fmt.Errorf("email service is disabled")
	}

	// Validate email provider configuration
	if es.Controller.Options.EmailProvider == "" {
		log.Printf("Email provider not configured")
		return fmt.Errorf("email provider not configured")
	}

	provider := strings.ToLower(es.Controller.Options.EmailProvider)
	if provider == "sendgrid" && es.Controller.Options.EmailSendGridAPIKey == "" {
		log.Printf("SendGrid API key not configured")
		return fmt.Errorf("SendGrid API key not configured")
	}
	if provider == "mailgun" && (es.Controller.Options.EmailMailgunAPIKey == "" || es.Controller.Options.EmailMailgunDomain == "") {
		log.Printf("Mailgun not properly configured - missing API key or domain")
		return fmt.Errorf("Mailgun not properly configured")
	}
	if provider == "smtp" && es.Controller.Options.EmailSmtpHost == "" {
		log.Printf("SMTP host not configured")
		return fmt.Errorf("SMTP host not configured")
	}
	if es.Controller.Options.EmailSmtpFromEmail == "" {
		log.Printf("From email address not configured")
		return fmt.Errorf("from email address not configured")
	}

	// Validate recipient email
	if toEmail == "" {
		return fmt.Errorf("recipient email address is required")
	}

	// Get branding
	branding := es.Controller.Options.Branding
	if branding == "" {
		branding = "ThinLine Radio"
	}

	// Get from name
	fromName := es.Controller.Options.EmailSmtpFromName
	if fromName == "" {
		fromName = branding
	}

	// Get logo URL (if uploaded by admin)
	var logoURL string
	if es.Controller.Options.EmailLogoFilename != "" {
		baseUrl := es.Controller.Options.BaseUrl
		if baseUrl == "" {
			baseUrl = "https://localhost:8080"
		} else {
			if strings.HasPrefix(baseUrl, "http://") {
				baseUrl = strings.Replace(baseUrl, "http://", "https://", 1)
			} else if !strings.HasPrefix(baseUrl, "https://") {
				baseUrl = "https://" + baseUrl
			}
		}
		logoURL = baseUrl + "/email-logo"
	}

	// Get border radius for logo
	borderRadius := es.Controller.Options.EmailLogoBorderRadius
	if borderRadius == "" {
		borderRadius = "0px"
	}

	// Generate test email HTML
	htmlBody := getTestEmailHTML(branding, logoURL, borderRadius)

	// Set up email headers
	fromEmail := es.Controller.Options.EmailSmtpFromEmail
	subject := fmt.Sprintf("📻 Test Email - %s", branding)

	// Extract domain for HELO
	domain := extractDomainFromEmail(fromEmail)

	log.Printf("📧 Sending test email to %s with HELO %s...", toEmail, domain)

	// Remove emojis from subject and body
	subject = removeEmojis(subject)
	htmlBody = removeEmojisFromHTML(htmlBody)

	// Send email using provider routing
	if err := es.sendEmail(fromName, fromEmail, toEmail, subject, htmlBody); err != nil {
		return err
	}

	log.Printf("✅ Test email sent successfully to %s", toEmail)
	return nil
}

// getTestEmailHTML generates the HTML content for test emails
func getTestEmailHTML(branding, logoURL, borderRadius string) string {
	if branding == "" {
		branding = "ThinLine Radio"
	}

	htmlTemplate := `<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Test Email - {{.Branding}}</title>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif;
            line-height: 1.6;
            color: #333;
            max-width: 600px;
            margin: 0 auto;
            padding: 20px;
            background-color: #f5f5f5;
        }
        .container {
            background-color: #ffffff;
            border-radius: 12px;
            padding: 0;
            box-shadow: 0 4px 12px rgba(0,0,0,0.1);
            overflow: hidden;
        }
        .header {
            background: #2c2c2c;
            color: white;
            text-align: center;
            padding: 40px 20px;
        }
        .logo-img {
            max-width: 200px;
            height: auto;
            margin: 0 auto 15px auto;
            display: block;
            border-radius: {{.BorderRadius}};
        }
        .logo-icon {
            font-size: 64px;
            margin: 0 auto 15px auto;
            display: block;
            text-align: center;
        }
        h1 {
            color: white;
            margin: 0;
            font-size: 28px;
            font-weight: 600;
        }
        .content {
            padding: 40px;
        }
        .content p {
            margin: 0 0 15px 0;
            color: #555;
            font-size: 16px;
        }
        .success-box {
            background-color: #d4edda;
            border: 1px solid #c3e6cb;
            border-radius: 8px;
            padding: 20px;
            margin: 20px 0;
            text-align: center;
        }
        .success-box h2 {
            color: #155724;
            margin: 0 0 10px 0;
            font-size: 24px;
        }
        .success-box p {
            color: #155724;
            margin: 0;
            font-size: 16px;
        }
        .footer {
            text-align: center;
            padding: 30px 40px;
            background-color: #f8f9fa;
            border-top: 1px solid #e9ecef;
        }
        .footer-icon {
            font-size: 32px;
            margin-bottom: 10px;
        }
        .footer p {
            margin: 5px 0;
            font-size: 13px;
            color: #6c757d;
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            {{if .LogoURL}}
                <img src="{{.LogoURL}}" alt="{{.Branding}}" class="logo-img">
            {{else}}
                <div class="logo-icon">📻</div>
            {{end}}
            <h1>{{.Branding}}</h1>
        </div>
        <div class="content">
            <div class="success-box">
                <h2>✅ Test Email Successful!</h2>
                <p>Your email configuration is working correctly.</p>
            </div>
            <p>This is a test email to verify that your SMTP settings are configured properly.</p>
            <p>If you received this email, it means:</p>
            <ul style="color: #555; font-size: 16px;">
                <li>Your SMTP server connection is working</li>
                <li>Your authentication credentials are correct</li>
                <li>Your email service is ready to send verification and notification emails</li>
            </ul>
            <p style="font-size: 14px; color: #6c757d; margin-top: 30px;">
                This test email was sent from the admin configuration page.
            </p>
        </div>
        <div class="footer">
            {{if .LogoURL}}
                <img src="{{.LogoURL}}" alt="{{.Branding}}" style="max-width: 100px; height: auto; display: block; margin: 0 auto; border-radius: {{.BorderRadius}};">
            {{else}}
                <div class="footer-icon">📻</div>
            {{end}}
        </div>
    </div>
</body>
</html>`

	tmpl, err := template.New("testEmail").Parse(htmlTemplate)
	if err != nil {
		log.Printf("Error parsing test email template: %v", err)
		return "<html><body><h1>Test Email</h1><p>Your email configuration is working correctly!</p></body></html>"
	}

	var buf bytes.Buffer
	data := struct {
		Branding     string
		LogoURL      string
		BorderRadius string
	}{
		Branding:     branding,
		LogoURL:      logoURL,
		BorderRadius: borderRadius,
	}

	if err := tmpl.Execute(&buf, data); err != nil {
		log.Printf("Error executing test email template: %v", err)
		return "<html><body><h1>Test Email</h1><p>Your email configuration is working correctly!</p></body></html>"
	}

	return buf.String()
}

// SendAppUpdateRequiredEmail notifies a user that their push notifications have stopped
// working because the app needs to be updated. Called when a legacy OneSignal device
// token is detected at push-send time.
func (es *EmailService) SendAppUpdateRequiredEmail(user *User) error {
	if !es.Controller.Options.EmailServiceEnabled {
		return fmt.Errorf("email service is disabled")
	}
	if es.Controller.Options.EmailProvider == "" {
		return fmt.Errorf("email provider not configured")
	}
	if es.Controller.Options.EmailSmtpFromEmail == "" {
		return fmt.Errorf("from email address not configured")
	}
	if user.Email == "" {
		return fmt.Errorf("user has no email address")
	}

	branding := es.Controller.Options.Branding
	if branding == "" {
		branding = "ThinLine Radio"
	}
	fromName := es.Controller.Options.EmailSmtpFromName
	if fromName == "" {
		fromName = branding
	}
	fromEmail := es.Controller.Options.EmailSmtpFromEmail

	var logoURL string
	if es.Controller.Options.EmailLogoFilename != "" {
		base := es.Controller.Options.BaseUrl
		if base == "" {
			base = "https://localhost:8080"
		} else if strings.HasPrefix(base, "http://") {
			base = strings.Replace(base, "http://", "https://", 1)
		} else if !strings.HasPrefix(base, "https://") {
			base = "https://" + base
		}
		logoURL = base + "/email-logo"
	}

	displayName := user.FirstName
	if displayName == "" {
		displayName = extractNameFromEmail(user.Email)
	}

	subject := fmt.Sprintf("Action Required: Update the %s App to Continue Receiving Alerts", branding)
	htmlBody := getAppUpdateRequiredEmailHTML(displayName, branding, logoURL)

	if err := es.sendEmail(fromName, fromEmail, user.Email, subject, htmlBody); err != nil {
		return err
	}

	log.Printf("App update required email sent to user %d (%s)", user.Id, user.Email)
	return nil
}

func getAppUpdateRequiredEmailHTML(displayName, branding, logoURL string) string {
	if branding == "" {
		branding = "ThinLine Radio"
	}

	logo := ""
	if logoURL != "" {
		logo = fmt.Sprintf(`<img src="%s" alt="%s" style="max-height:60px;max-width:200px;margin-bottom:12px;">`, logoURL, branding)
	} else {
		logo = fmt.Sprintf(`<div style="font-size:48px;margin-bottom:8px;">📻</div>`)
	}

	greeting := "Hello"
	if displayName != "" {
		greeting = fmt.Sprintf("Hi %s", displayName)
	}

	return fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <title>Update Required – %s</title>
</head>
<body style="margin:0;padding:0;background:#f4f4f4;font-family:'Helvetica Neue',Helvetica,Arial,sans-serif;">
  <table width="100%%" cellpadding="0" cellspacing="0" style="background:#f4f4f4;padding:40px 0;">
    <tr>
      <td align="center">
        <table width="560" cellpadding="0" cellspacing="0" style="background:#ffffff;border-radius:8px;overflow:hidden;box-shadow:0 2px 8px rgba(0,0,0,0.08);">

          <!-- Header -->
          <tr>
            <td align="center" style="background:#1a1a2e;padding:32px 40px;">
              %s
              <h1 style="margin:0;color:#ffffff;font-size:22px;font-weight:600;letter-spacing:0.5px;">%s</h1>
            </td>
          </tr>

          <!-- Body -->
          <tr>
            <td style="padding:36px 40px;">
              <p style="margin:0 0 16px;font-size:16px;color:#333;line-height:1.6;">%s,</p>

              <p style="margin:0 0 16px;font-size:16px;color:#333;line-height:1.6;">
                We noticed that your push notifications for <strong>%s</strong> are no longer being delivered to one or more of your devices.
              </p>

              <p style="margin:0 0 16px;font-size:16px;color:#333;line-height:1.6;">
                This happened because our app has upgraded its push notification system and <strong>your app version is out of date</strong>. We've removed the stale notification token from your account.
              </p>

              <!-- Action box -->
              <table width="100%%" cellpadding="0" cellspacing="0" style="margin:24px 0;">
                <tr>
                  <td style="background:#fff3cd;border-left:4px solid #f59e0b;border-radius:4px;padding:16px 20px;">
                    <p style="margin:0;font-size:15px;color:#92400e;font-weight:600;">Action required</p>
                    <p style="margin:6px 0 0;font-size:14px;color:#78350f;line-height:1.5;">
                      Please update the <strong>%s</strong> app from the App Store or Google Play Store to restore your push notifications.
                    </p>
                  </td>
                </tr>
              </table>

              <p style="margin:0 0 16px;font-size:16px;color:#333;line-height:1.6;">
                After updating, simply open the app and your alerts will automatically re-register. You don't need to do anything else.
              </p>

              <p style="margin:24px 0 0;font-size:14px;color:#666;line-height:1.6;">
                If you believe this was sent in error or need help, please contact your system administrator.
              </p>
            </td>
          </tr>

          <!-- Footer -->
          <tr>
            <td style="background:#f8f8f8;padding:20px 40px;border-top:1px solid #e8e8e8;">
              <p style="margin:0;font-size:12px;color:#999;text-align:center;">
                This is an automated message from %s. Please do not reply to this email.
              </p>
            </td>
          </tr>

        </table>
      </td>
    </tr>
  </table>
</body>
</html>`,
		branding,
		logo,
		branding,
		greeting,
		branding,
		branding,
		branding,
	)
}
