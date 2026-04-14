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
	"html/template"
	texttemplate "text/template"
)

// EmailTemplateData holds the data for email templates
type EmailTemplateData struct {
	UserEmail       string
	VerificationURL string
	BaseURL         string
}

// EmailTemplates manages email templates
type EmailTemplates struct {
	verificationHTML *template.Template
	verificationText *texttemplate.Template
}

// NewEmailTemplates creates a new EmailTemplates instance
func NewEmailTemplates() (*EmailTemplates, error) {
	et := &EmailTemplates{}

	// Load HTML template
	htmlTmpl, err := template.ParseFiles("templates/email_verification.html")
	if err != nil {
		return nil, err
	}
	et.verificationHTML = htmlTmpl

	// Load text template
	textTmpl, err := texttemplate.ParseFiles("templates/email_verification.txt")
	if err != nil {
		return nil, err
	}
	et.verificationText = textTmpl

	return et, nil
}

// GenerateVerificationEmail generates both HTML and text versions of verification email
func (et *EmailTemplates) GenerateVerificationEmail(data EmailTemplateData) (htmlContent, textContent string, err error) {
	// Generate HTML content
	var htmlBuf bytes.Buffer
	if err := et.verificationHTML.Execute(&htmlBuf, data); err != nil {
		return "", "", err
	}
	htmlContent = htmlBuf.String()

	// Generate text content
	var textBuf bytes.Buffer
	if err := et.verificationText.Execute(&textBuf, data); err != nil {
		return "", "", err
	}
	textContent = textBuf.String()

	return htmlContent, textContent, nil
}

// GetVerificationEmailSubject returns the subject line for verification emails
func (et *EmailTemplates) GetVerificationEmailSubject() string {
	return "📻 Verify Your Email - ThinLine Radio"
}
