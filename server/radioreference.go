// Copyright (C) 2025 Thinline Dynamic Solutions
//
// IMPORTANT: Radio Reference API Key Setup
// ========================================
// 1. Apply for an API key at: https://www.radioreference.com/account/api
// 2. The API key is automatically fetched from the relay server on startup
//    - Or set via environment variable: RADIO_REFERENCE_API_KEY=your_key_here
// 3. Users provide their own username/password in the admin interface
// 4. The API key identifies YOUR APPLICATION, not the user
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR ANY PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>.

package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/antchfx/xmlquery"
)

const (
	RADIO_REFERENCE_BASE_URL = "http://api.radioreference.com/soap2/"
)

type RadioReferenceService struct {
	username string
	password string
	appKey   string
	client   *http.Client
}

// SOAP Response structures based on official API documentation
type RadioReferenceUserInfo struct {
	Username       string `xml:"username"`
	ExpirationDate string `xml:"expirationDate"`
	SubExpireDate  string `xml:"subExpireDate"`
}

type RadioReferenceSystem struct {
	ID          int    `xml:"id" json:"id"`
	Name        string `xml:"name" json:"name"`
	Type        string `xml:"type" json:"type"`
	City        string `xml:"city" json:"city"`
	County      string `xml:"county" json:"county"`
	State       string `xml:"state" json:"state"`
	Country     string `xml:"country" json:"country"`
	LastUpdated string `xml:"lastUpdated" json:"lastUpdated"`
}

type RadioReferenceTalkgroup struct {
	ID          int    `xml:"id" json:"id"`
	AlphaTag    string `xml:"alphaTag" json:"alphaTag"`
	Description string `xml:"description" json:"description"`
	Group       string `xml:"group" json:"group"`
	Tag         string `xml:"tag" json:"tag"`
	Enc         int    `xml:"enc" json:"enc"`
}

type RadioReferenceTalkgroupCategory struct {
	ID          int    `xml:"id" json:"id"`
	Name        string `xml:"name" json:"name"`
	Description string `xml:"description" json:"description"`
}

type RadioReferenceSite struct {
	ID          string    `xml:"id" json:"id"`     // This will store siteNumber formatted as 3 digits
	Name        string    `xml:"name" json:"name"` // This will store siteDescr
	Latitude    float64   `xml:"latitude" json:"latitude"`
	Longitude   float64   `xml:"longitude" json:"longitude"`
	CountyID    int       `xml:"countyId" json:"countyId"`       // This will store siteCtid
	CountyName  string    `xml:"countyName" json:"countyName"`   // This will store countyName
	RFSS        int       `xml:"rfss" json:"rfss"`               // This will store rfss
	Frequencies []float64 `xml:"frequencies" json:"frequencies"` // Site frequencies
}

type RadioReferenceFrequency struct {
	ID          int     `xml:"id"`
	Frequency   float64 `xml:"frequency"`
	Type        string  `xml:"type"`
	Description string  `xml:"description"`
}

// Generic id/name item for dropdowns
type RadioReferenceItem struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// Universal SOAP envelope structure that handles all namespace variations
type SOAPEnvelope struct {
	XMLName xml.Name `xml:"Envelope"`
	Body    SOAPBody `xml:"Body"`
}

type SOAPBody struct {
	Content []byte `xml:",innerxml"`
}

// Alternative SOAP envelope structure for SOAP-ENV namespace
type SOAPEnvelopeAlt struct {
	XMLName xml.Name    `xml:"SOAP-ENV:Envelope"`
	Body    SOAPBodyAlt `xml:"SOAP-ENV:Body"`
}

type SOAPBodyAlt struct {
	Content []byte `xml:",innerxml"`
}

// Alternative SOAP envelope structure for soap namespace
type SOAPEnvelopeSoap struct {
	XMLName xml.Name     `xml:"soap:Envelope"`
	Body    SOAPBodySoap `xml:"soap:Body"`
}

type SOAPBodySoap struct {
	Content []byte `xml:",innerxml"`
}

type SOAPFault struct {
	XMLName     xml.Name `xml:"Fault"`
	FaultCode   string   `xml:"faultcode"`
	FaultString string   `xml:"faultstring"`
}

// extractSOAPBody attempts to parse SOAP response using multiple namespace formats
// and returns the body content regardless of which format is used
func extractSOAPBody(xmlBytes []byte) ([]byte, error) {
	// Try different SOAP envelope formats

	// Try standard Envelope format
	var envelope SOAPEnvelope
	if err := xml.Unmarshal(xmlBytes, &envelope); err == nil && len(envelope.Body.Content) > 0 {
		return envelope.Body.Content, nil
	}

	// Try SOAP-ENV:Envelope format
	var envelopeAlt SOAPEnvelopeAlt
	if err := xml.Unmarshal(xmlBytes, &envelopeAlt); err == nil && len(envelopeAlt.Body.Content) > 0 {
		return envelopeAlt.Body.Content, nil
	}

	// Try soap:Envelope format
	var envelopeSoap SOAPEnvelopeSoap
	if err := xml.Unmarshal(xmlBytes, &envelopeSoap); err == nil && len(envelopeSoap.Body.Content) > 0 {
		return envelopeSoap.Body.Content, nil
	}

	// If all parsing attempts fail, return the original XML for manual parsing
	return xmlBytes, fmt.Errorf("failed to parse SOAP envelope with any known format")
}

func NewRadioReferenceService(username, password, appKey string) *RadioReferenceService {
	// If no API key provided, try environment variable
	if appKey == "" {
		appKey = os.Getenv("RADIO_REFERENCE_API_KEY")
	}

	return &RadioReferenceService{
		username: username,
		password: password,
		appKey:   appKey,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (rr *RadioReferenceService) TestConnection() (*RadioReferenceUserInfo, error) {

	// First perform authentication validation
	if err := rr.AuthenticateAndValidate(); err != nil {
		return nil, err
	}

	// If authentication passed, return user info using a simple SOAP envelope (no namespaces) like the Java client
	body := fmt.Sprintf(`<getUserData xmlns="http://api.radioreference.com/soap2">
      <authInfo>
        <appKey>%s</appKey>
        <username>%s</username>
        <password>%s</password>
        <version>18</version>
        <style>doc</style>
      </authInfo>
    </getUserData>`, rr.appKey, rr.username, rr.password)
	soapRequest := rr.buildSimpleEnvelope(body)

	resp, err := rr.makeRequestSimple(soapRequest)
	if err != nil {
		return nil, err
	}

	// Parse the SOAP envelope to get user info
	bodyContent, err := extractSOAPBody(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to extract SOAP body: %v", err)
	}

	// Response body shape: <ns1:getUserDataResponse><return>...UserInfo...</return></ns1:getUserDataResponse>
	type getUserDataResponse struct {
		Return RadioReferenceUserInfo `xml:"return"`
	}

	var gud getUserDataResponse
	if err := xml.Unmarshal(bodyContent, &gud); err != nil {
		return nil, fmt.Errorf("failed to parse getUserDataResponse: %v", err)
	}

	if gud.Return.Username == "" {
		return nil, fmt.Errorf("invalid response: missing username")
	}

	return &gud.Return, nil
}

// AuthenticateAndValidate performs a sanity check using getUserData to validate credentials
func (rr *RadioReferenceService) AuthenticateAndValidate() error {

	// Simple envelope like Java client
	body := fmt.Sprintf(`<getUserData xmlns="http://api.radioreference.com/soap2">
      <authInfo>
        <appKey>%s</appKey>
        <username>%s</username>
        <password>%s</password>
        <version>18</version>
        <style>doc</style>
      </authInfo>
    </getUserData>`, rr.appKey, rr.username, rr.password)
	soapRequest := rr.buildSimpleEnvelope(body)

	resp, err := rr.makeRequestSimple(soapRequest)
	if err != nil {
		return fmt.Errorf("authentication check failed: %v", err)
	}

	// Check for SOAP faults first
	var fault SOAPFault
	if err := xml.Unmarshal(resp, &fault); err == nil && (fault.FaultCode != "" || fault.FaultString != "") {

		// Handle specific authentication errors
		if strings.Contains(strings.ToLower(fault.FaultCode), "auth") ||
			strings.Contains(strings.ToLower(fault.FaultString), "invalid username") ||
			strings.Contains(strings.ToLower(fault.FaultString), "invalid password") {
			return fmt.Errorf("authentication failed: invalid username, password, or API key")
		}

		// Handle expired account
		if strings.Contains(strings.ToLower(fault.FaultString), "expired") ||
			strings.Contains(strings.ToLower(fault.FaultString), "premium") {
			return fmt.Errorf("account expired or premium access required: %s", fault.FaultString)
		}

		return fmt.Errorf("authentication check failed: %s - %s", fault.FaultCode, fault.FaultString)
	}

	// Parse the SOAP envelope to validate response structure
	bodyContent, err := extractSOAPBody(resp)
	if err != nil {
		return fmt.Errorf("failed to parse authentication response: %v", err)
	}

	// Try to parse the user data response
	type getUserDataResponse struct {
		Return RadioReferenceUserInfo `xml:"return"`
	}

	var gud getUserDataResponse
	if err := xml.Unmarshal(bodyContent, &gud); err != nil {
		return fmt.Errorf("failed to parse getUserData response: %v", err)
	}

	if gud.Return.Username == "" {
		return fmt.Errorf("authentication response missing username")
	}

	expiry := gud.Return.ExpirationDate
	if expiry == "" {
		expiry = gud.Return.SubExpireDate
	}

	// Warn for non-premium accounts; some endpoints may still fail with AUTH
	if strings.Contains(strings.ToLower(expiry), "feed provider") {
		log.Printf("WARNING: RadioReference account appears to be Feed Provider (non-premium); some API methods may return AUTH faults")
	}
	return nil
}

// ----- Dropdown data methods -----

// GetCountries retrieves all countries
func (rr *RadioReferenceService) GetCountries() ([]RadioReferenceItem, error) {
	// Perform authentication sanity check first
	if err := rr.AuthenticateAndValidate(); err != nil {
		return nil, fmt.Errorf("authentication validation failed: %v", err)
	}

	bodyInner := fmt.Sprintf(`<soap:getCountryList>
      <authInfo>
        <style>doc</style>
        <version>18</version>
        <password>%s</password>
        <username>%s</username>
        <appKey>%s</appKey>
      </authInfo>
    </soap:getCountryList>`, rr.password, rr.username, rr.appKey)
	soap := rr.buildSimpleEnvelope(bodyInner)

	body, err := rr.makeRequestSimple(soap)
	if err != nil {
		return nil, err
	}

	// Debug: Log the full response for countries

	// Try the generic parser first
	items := parseIdNameList(body, []string{"countryId", "coid", "id", "countryId"}, []string{"name", "country", "countryName"})

	// If generic parser failed, try specific country parsing
	if len(items) == 0 {
		items = parseCountriesResponse(body)
	}

	return items, nil
}

// GetStates returns states for a country via getCountryInfo
func (rr *RadioReferenceService) GetStates(countryID int) ([]RadioReferenceItem, error) {
	// Perform authentication sanity check first
	if err := rr.AuthenticateAndValidate(); err != nil {
		return nil, fmt.Errorf("authentication validation failed: %v", err)
	}

	body := fmt.Sprintf(`<soap:getCountryInfo>
      <request>%d</request>
      <authInfo>
        <style>doc</style>
        <version>18</version>
        <password>%s</password>
        <username>%s</username>
        <appKey>%s</appKey>
      </authInfo>
    </soap:getCountryInfo>`, countryID, rr.password, rr.username, rr.appKey)
	soap := rr.buildSimpleEnvelope(body)

	// Debug: Log the SOAP request being sent

	// Send without SOAPAction (matches Java client)
	bodyResp, err := rr.makeRequestSimple(soap)
	if err != nil {
		return nil, err
	}

	// Debug: Log the full response for states

	// Try the generic parser first
	items := parseIdNameList(bodyResp, []string{"stateId", "stid", "id"}, []string{"stateName", "name", "state"})

	// If generic parser failed, try specific state parsing
	if len(items) == 0 {
		items = parseStatesResponse(bodyResp)
	}

	return items, nil
}

// GetCounties returns counties for a state via getStateInfo
func (rr *RadioReferenceService) GetCounties(stateID int) ([]RadioReferenceItem, error) {
	body := fmt.Sprintf(`<soap:getStateInfo>
      <request>%d</request>
      <authInfo>
        <style>doc</style>
        <version>18</version>
        <password>%s</password>
        <username>%s</username>
        <appKey>%s</appKey>
      </authInfo>
    </soap:getStateInfo>`, stateID, rr.password, rr.username, rr.appKey)
	soap := rr.buildSimpleEnvelope(body)

	// Debug: Log the SOAP request being sent

	bodyResp, err := rr.makeRequestSimple(soap)
	if err != nil {
		return nil, err
	}

	// Debug: Log the full response for counties

	// Try the generic parser first
	items := parseIdNameList(bodyResp, []string{"countyId", "ctid", "id"}, []string{"countyName", "name", "county"})

	// If generic parser failed, try specific county parsing
	if len(items) == 0 {
		items = parseCountiesResponse(bodyResp)
	}

	return items, nil
}

// GetSystemsByCounty returns systems for a county via getCountyInfo
func (rr *RadioReferenceService) GetSystemsByCounty(countyID int) ([]RadioReferenceItem, error) {
	body := fmt.Sprintf(`<soap:getCountyInfo>
      <request>%d</request>
      <authInfo>
        <style>doc</style>
        <version>18</version>
        <password>%s</password>
        <username>%s</username>
        <appKey>%s</appKey>
      </authInfo>
    </soap:getCountyInfo>`, countyID, rr.password, rr.username, rr.appKey)
	soap := rr.buildSimpleEnvelope(body)

	// Debug: Log the SOAP request being sent

	bodyResp, err := rr.makeRequestSimple(soap)
	if err != nil {
		return nil, err
	}

	// Debug: Log the full response for systems

	// Try the generic parser first
	items := parseIdNameList(bodyResp, []string{"systemId", "sid", "id"}, []string{"sName", "name", "system"})

	// If generic parser failed, try specific system parsing
	if len(items) == 0 {
		items = parseSystemsResponse(bodyResp)
	}

	return items, nil
}

// parseCountriesResponse specifically parses the Radio Reference countries response
func parseCountriesResponse(xmlBytes []byte) []RadioReferenceItem {
	var items []RadioReferenceItem
	dec := xml.NewDecoder(strings.NewReader(string(xmlBytes)))

	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}

		switch t := tok.(type) {
		case xml.StartElement:
			// Look for <item> elements that contain country data
			if t.Name.Local == "item" {
				var country struct {
					COID        int    `xml:"coid"`
					CountryName string `xml:"countryName"`
				}

				if err := dec.DecodeElement(&country, &t); err == nil {
					if country.COID > 0 && country.CountryName != "" {
						items = append(items, RadioReferenceItem{
							ID:   country.COID,
							Name: country.CountryName,
						})
					}
				}
			}
		}
	}

	return items
}

// parseStatesResponse specifically parses the Radio Reference states response from getCountryInfo
func parseStatesResponse(xmlBytes []byte) []RadioReferenceItem {
	var items []RadioReferenceItem

	// Try to parse the full CountryInfo response structure
	type CountryInfo struct {
		StateList struct {
			Items []struct {
				STID      int    `xml:"stid"`
				StateName string `xml:"stateName"`
				StateCode string `xml:"stateCode"`
			} `xml:"item"`
		} `xml:"stateList"`
	}

	var countryInfo CountryInfo
	if err := xml.Unmarshal(xmlBytes, &countryInfo); err == nil {
		for _, state := range countryInfo.StateList.Items {
			if state.STID > 0 && state.StateName != "" {
				items = append(items, RadioReferenceItem{
					ID:   state.STID,
					Name: state.StateName,
				})
			}
		}
		if len(items) > 0 {
			return items
		}
	}

	// Fallback: try to parse individual State elements
	dec := xml.NewDecoder(strings.NewReader(string(xmlBytes)))
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}

		switch t := tok.(type) {
		case xml.StartElement:
			// Look for State elements
			if t.Name.Local == "State" {
				var state struct {
					STID      int    `xml:"stid"`
					StateName string `xml:"stateName"`
					StateCode string `xml:"stateCode"`
				}

				if err := dec.DecodeElement(&state, &t); err == nil {
					if state.STID > 0 && state.StateName != "" {
						items = append(items, RadioReferenceItem{
							ID:   state.STID,
							Name: state.StateName,
						})
					}
				}
			}
		}
	}

	return items
}

// parseCountiesResponse specifically parses the Radio Reference counties response from getStateInfo
func parseCountiesResponse(xmlBytes []byte) []RadioReferenceItem {
	var items []RadioReferenceItem
	dec := xml.NewDecoder(strings.NewReader(string(xmlBytes)))

	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}

		switch t := tok.(type) {
		case xml.StartElement:
			// Look for <item> elements that contain county data
			if t.Name.Local == "item" {
				var county struct {
					CTID         int    `xml:"ctid"`
					CountyName   string `xml:"countyName"`
					CountyHeader string `xml:"countyHeader"`
				}

				if err := dec.DecodeElement(&county, &t); err == nil {
					if county.CTID > 0 && county.CountyName != "" {
						items = append(items, RadioReferenceItem{
							ID:   county.CTID,
							Name: county.CountyName,
						})
					}
				}
			}
		}
	}

	return items
}

// parseSystemsResponse specifically parses the Radio Reference systems response from getCountyInfo
func parseSystemsResponse(xmlBytes []byte) []RadioReferenceItem {
	var items []RadioReferenceItem
	dec := xml.NewDecoder(strings.NewReader(string(xmlBytes)))

	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}

		switch t := tok.(type) {
		case xml.StartElement:
			// Look for <item> elements that contain system data
			if t.Name.Local == "item" {
				var system struct {
					SID   int    `xml:"sid"`
					SName string `xml:"sName"`
					SType int    `xml:"sType"`
					SCity string `xml:"sCity"`
				}

				if err := dec.DecodeElement(&system, &t); err == nil {
					if system.SID > 0 && system.SName != "" {
						items = append(items, RadioReferenceItem{
							ID:   system.SID,
							Name: system.SName,
						})
					}
				}
			}
		}
	}

	return items
}

// parseTalkgroupsFromXML parses talkgroup data from XML when the structured parser fails
// This is an enhanced version that extracts all talkgroup fields including encryption
func parseTalkgroupsFromXML(xmlBytes []byte) []RadioReferenceTalkgroup {
	var talkgroups []RadioReferenceTalkgroup
	dec := xml.NewDecoder(strings.NewReader(string(xmlBytes)))

	var currentTalkgroup *RadioReferenceTalkgroup

	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}

		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "item":
				// Start of a new talkgroup item
				if currentTalkgroup != nil {
					talkgroups = append(talkgroups, *currentTalkgroup)
				}
				currentTalkgroup = &RadioReferenceTalkgroup{}

			case "tgId":
				var v string
				if err := dec.DecodeElement(&v, &t); err == nil {
					if id, convErr := strconv.Atoi(strings.TrimSpace(v)); convErr == nil {
						currentTalkgroup.ID = id
					}
				}

			case "tgDec":
				var v string
				if err := dec.DecodeElement(&v, &t); err == nil {
					if id, convErr := strconv.Atoi(strings.TrimSpace(v)); convErr == nil {
						currentTalkgroup.ID = id // Use tgDec as the ID
					}
				}

			case "tgDescr":
				var v string
				if err := dec.DecodeElement(&v, &t); err == nil {
					currentTalkgroup.Description = strings.TrimSpace(v)
				}

			case "tgAlpha":
				var v string
				if err := dec.DecodeElement(&v, &t); err == nil {
					currentTalkgroup.AlphaTag = strings.TrimSpace(v)
				}

			case "enc":
				var v string
				if err := dec.DecodeElement(&v, &t); err == nil {
					if enc, convErr := strconv.Atoi(strings.TrimSpace(v)); convErr == nil {
						currentTalkgroup.Enc = enc
					}
				}
			}

		case xml.EndElement:
			if t.Name.Local == "item" && currentTalkgroup != nil {
				// End of talkgroup item, ensure we have required fields
				if currentTalkgroup.ID > 0 && (currentTalkgroup.Description != "" || currentTalkgroup.AlphaTag != "") {
					// If no description, use alpha tag
					if currentTalkgroup.Description == "" {
						currentTalkgroup.Description = currentTalkgroup.AlphaTag
					}
					talkgroups = append(talkgroups, *currentTalkgroup)
				}
				currentTalkgroup = nil
			}
		}
	}

	// Don't forget the last talkgroup if there is one
	if currentTalkgroup != nil && currentTalkgroup.ID > 0 && (currentTalkgroup.Description != "" || currentTalkgroup.AlphaTag != "") {
		if currentTalkgroup.Description == "" {
			currentTalkgroup.Description = currentTalkgroup.AlphaTag
		}
		talkgroups = append(talkgroups, *currentTalkgroup)
	}

	return talkgroups
}

// parseIdNameList parses an XML document and extracts id/name pairs regardless of namespace/wrappers.
// This function is specifically designed to handle Radio Reference API responses with namespaces.
func parseIdNameList(xmlBytes []byte, idTags []string, nameTags []string) []RadioReferenceItem {
	var items []RadioReferenceItem
	dec := xml.NewDecoder(strings.NewReader(string(xmlBytes)))
	var (
		currentID   *int
		currentName *string
		stack       []string
	)
	commit := func() {
		if currentID != nil && currentName != nil {
			items = append(items, RadioReferenceItem{ID: *currentID, Name: *currentName})
			currentID = nil
			currentName = nil
		}
	}
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			stack = append(stack, t.Name.Local)
			// capture id - check both local name and full name for namespace handling
			for _, tag := range idTags {
				if t.Name.Local == tag || strings.Contains(t.Name.Space, tag) {
					var v string
					_ = dec.DecodeElement(&v, &t)
					if id, convErr := strconv.Atoi(strings.TrimSpace(v)); convErr == nil {
						currentID = &id
					}
					// pop after DecodeElement consumes end
					if len(stack) > 0 {
						stack = stack[:len(stack)-1]
					}
					commit()
					break
				}
			}
			// capture name - check both local name and full name for namespace handling
			for _, tag := range nameTags {
				if t.Name.Local == tag || strings.Contains(t.Name.Space, tag) {
					var v string
					_ = dec.DecodeElement(&v, &t)
					s := strings.TrimSpace(v)
					currentName = &s
					if len(stack) > 0 {
						stack = stack[:len(stack)-1]
					}
					commit()
					break
				}
			}
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	// deduplicate by id, keep first
	byID := map[int]string{}
	var out []RadioReferenceItem
	for _, it := range items {
		if it.ID == 0 || it.Name == "" {
			continue
		}
		if _, ok := byID[it.ID]; !ok {
			byID[it.ID] = it.Name
			out = append(out, it)
		}
	}
	return out
}

func (rr *RadioReferenceService) GetSystem(systemID int) (*RadioReferenceSystem, error) {
	body := fmt.Sprintf(`<soap:getTrsDetails>
		<sid>%d</sid>
		<authInfo>
			<version>18</version>
			<style>doc</style>
			<password>%s</password>
			<username>%s</username>
			<appKey>%s</appKey>
		</authInfo>
	</soap:getTrsDetails>`, systemID, rr.password, rr.username, rr.appKey)
	soapRequest := rr.buildSimpleEnvelope(body)

	resp, err := rr.makeRequestSimple(soapRequest)
	if err != nil {
		return nil, err
	}

	var fault SOAPFault
	if err := xml.Unmarshal(resp, &fault); err == nil && fault.FaultCode != "" {
		return nil, fmt.Errorf("SOAP fault: %s - %s", fault.FaultCode, fault.FaultString)
	}

	// Parse the SOAP envelope to get the body content
	bodyContent, err := extractSOAPBody(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to parse SOAP envelope: %v", err)
	}

	// Parse the actual system response structure
	type getTrsDetailsResponse struct {
		Return struct {
			SName   string `xml:"sName"`
			SType   int    `xml:"sType"`
			SFlavor int    `xml:"sFlavor"`
			SVoice  int    `xml:"sVoice"`
			SCity   string `xml:"sCity"`
			SCounty struct {
				Items []struct {
					CTID int `xml:"ctid"`
				} `xml:"item"`
			} `xml:"sCounty"`
			SState struct {
				Items []struct {
					STID int `xml:"stid"`
				} `xml:"item"`
			} `xml:"sState"`
			SCountry struct {
				Items []struct {
					COID int `xml:"coid"`
				} `xml:"item"`
			} `xml:"sCountry"`
		} `xml:"return"`
	}

	var response getTrsDetailsResponse
	if err := xml.Unmarshal(bodyContent, &response); err != nil {
		return nil, fmt.Errorf("failed to parse system response: %v", err)
	}

	// Convert to RadioReferenceSystem
	system := &RadioReferenceSystem{
		ID:          0, // System ID not in this response
		Name:        response.Return.SName,
		Type:        fmt.Sprintf("%d", response.Return.SType),
		City:        response.Return.SCity,
		County:      "",
		State:       "",
		Country:     "",
		LastUpdated: "",
	}

	// Add county info if available
	if len(response.Return.SCounty.Items) > 0 {
		system.County = fmt.Sprintf("%d", response.Return.SCounty.Items[0].CTID)
	}

	// Add state info if available
	if len(response.Return.SState.Items) > 0 {
		system.State = fmt.Sprintf("%d", response.Return.SState.Items[0].STID)
	}

	// Add country info if available
	if len(response.Return.SCountry.Items) > 0 {
		system.Country = fmt.Sprintf("%d", response.Return.SCountry.Items[0].COID)
	}

	return system, nil
}

// GetSystemType gets the system type using the exact SDRTrunk format
func (rr *RadioReferenceService) GetSystemType() (string, error) {
	body := `<soap:getTrsType><request/><authInfo><version>18</version><style>doc</style><password>%s</password><username>%s</username><appKey>%s</appKey></authInfo></soap:getTrsType>`
	body = fmt.Sprintf(body, rr.password, rr.username, rr.appKey)
	soapRequest := rr.buildSimpleEnvelope(body)

	resp, err := rr.makeRequestSimple(soapRequest)
	if err != nil {
		return "", err
	}

	var fault SOAPFault
	if err := xml.Unmarshal(resp, &fault); err == nil && fault.FaultCode != "" {
		return "", fmt.Errorf("SOAP fault: %s - %s", fault.FaultCode, fault.FaultString)
	}

	// Parse the SOAP envelope to get the body content
	bodyContent, err := extractSOAPBody(resp)
	if err != nil {
		return "", fmt.Errorf("failed to parse SOAP envelope: %v", err)
	}

	// Use the generic parser that works for countries, states, counties, and systems
	items := parseIdNameList(bodyContent, []string{"sType", "id"}, []string{"sTypeDescr", "description", "name"})

	// Return the first type description or empty string
	if len(items) > 0 {
		return items[0].Name, nil
	}
	return "", nil
}

// GetSystemFlavor gets the system flavor using the exact SDRTrunk format
func (rr *RadioReferenceService) GetSystemFlavor() (string, error) {
	body := `<soap:getTrsFlavor><request/><authInfo><version>18</version><style>doc</style><password>%s</password><username>%s</username><appKey>%s</appKey></authInfo></soap:getTrsFlavor>`
	body = fmt.Sprintf(body, rr.password, rr.username, rr.appKey)
	soapRequest := rr.buildSimpleEnvelope(body)

	resp, err := rr.makeRequestSimple(soapRequest)
	if err != nil {
		return "", err
	}

	var fault SOAPFault
	if err := xml.Unmarshal(resp, &fault); err == nil && fault.FaultCode != "" {
		return "", fmt.Errorf("SOAP fault: %s - %s", fault.FaultCode, fault.FaultString)
	}

	// Parse the SOAP envelope to get the body content
	bodyContent, err := extractSOAPBody(resp)
	if err != nil {
		return "", fmt.Errorf("failed to parse SOAP envelope: %v", err)
	}

	// Use the generic parser that works for countries, states, counties, and systems
	items := parseIdNameList(bodyContent, []string{"sFlavor", "id"}, []string{"sFlavorDescr", "description", "name"})

	// Return the first flavor description or empty string
	if len(items) > 0 {
		return items[0].Name, nil
	}
	return "", nil
}

// GetSystemVoice gets the system voice information using the exact SDRTrunk format
func (rr *RadioReferenceService) GetSystemVoice() (string, error) {
	body := `<soap:getTrsVoice><request/><authInfo><version>18</version><style>doc</style><password>%s</password><username>%s</username><appKey>%s</appKey></authInfo></soap:getTrsVoice>`
	body = fmt.Sprintf(body, rr.password, rr.username, rr.appKey)
	soapRequest := rr.buildSimpleEnvelope(body)

	resp, err := rr.makeRequestSimple(soapRequest)
	if err != nil {
		return "", err
	}

	var fault SOAPFault
	if err := xml.Unmarshal(resp, &fault); err == nil && fault.FaultCode != "" {
		return "", fmt.Errorf("SOAP fault: %s - %s", fault.FaultCode, fault.FaultString)
	}

	// Parse the SOAP envelope to get the body content
	bodyContent, err := extractSOAPBody(resp)
	if err != nil {
		return "", fmt.Errorf("failed to parse SOAP envelope: %v", err)
	}

	// Use the generic parser that works for countries, states, counties, and systems
	items := parseIdNameList(bodyContent, []string{"sVoice", "id"}, []string{"sVoiceDescr", "description", "name"})

	// Return the first voice description or empty string
	if len(items) > 0 {
		return items[0].Name, nil
	}
	return "", nil
}

// GetSystemTags gets the system tags using the exact SDRTrunk format
func (rr *RadioReferenceService) GetSystemTags() ([]string, error) {
	body := `<soap:getTag><request/><authInfo><version>18</version><style>doc</style><password>%s</password><username>%s</username><appKey>%s</appKey></authInfo></soap:getTag>`
	body = fmt.Sprintf(body, rr.password, rr.username, rr.appKey)
	soapRequest := rr.buildSimpleEnvelope(body)

	resp, err := rr.makeRequestSimple(soapRequest)
	if err != nil {
		return nil, err
	}

	var fault SOAPFault
	if err := xml.Unmarshal(resp, &fault); err == nil && fault.FaultCode != "" {
		return nil, fmt.Errorf("SOAP fault: %s - %s", fault.FaultCode, fault.FaultString)
	}

	// Parse the SOAP envelope to get the body content
	bodyContent, err := extractSOAPBody(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to parse SOAP envelope: %v", err)
	}

	// Use the generic parser that works for countries, states, counties, and systems
	items := parseIdNameList(bodyContent, []string{"tagId", "id"}, []string{"tagDescr", "description", "name"})

	// Convert to string slice
	var tags []string
	for _, item := range items {
		if item.ID > 0 && item.Name != "" {
			tags = append(tags, item.Name)
		}
	}

	return tags, nil
}

// GetSystemTagsMap gets the system tags as a map of tag ID to tag name
func (rr *RadioReferenceService) GetSystemTagsMap() (map[int]string, error) {
	body := `<soap:getTag><request/><authInfo><version>18</version><style>doc</style><password>%s</password><username>%s</username><appKey>%s</appKey></authInfo></soap:getTag>`
	body = fmt.Sprintf(body, rr.password, rr.username, rr.appKey)
	soapRequest := rr.buildSimpleEnvelope(body)

	resp, err := rr.makeRequestSimple(soapRequest)
	if err != nil {
		return nil, err
	}

	var fault SOAPFault
	if err := xml.Unmarshal(resp, &fault); err == nil && fault.FaultCode != "" {
		return nil, fmt.Errorf("SOAP fault: %s - %s", fault.FaultCode, fault.FaultString)
	}

	// Parse the SOAP envelope to get the body content
	bodyContent, err := extractSOAPBody(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to parse SOAP envelope: %v", err)
	}

	// Use the generic parser that works for countries, states, counties, and systems
	items := parseIdNameList(bodyContent, []string{"tagId", "id"}, []string{"tagDescr", "description", "name"})

	// Convert to map of tag ID to tag name
	tagMap := make(map[int]string)
	for _, item := range items {
		if item.ID > 0 && item.Name != "" {
			tagMap[item.ID] = item.Name
		}
	}

	return tagMap, nil
}

// GetSystemSites gets the system sites using the exact SDRTrunk format
func (rr *RadioReferenceService) GetSystemSites(systemID int) ([]RadioReferenceSite, error) {
	body := fmt.Sprintf(`<soap:getTrsSites>
		<sid>%d</sid>
		<authInfo>
			<version>18</version>
			<style>doc</style>
			<password>%s</password>
			<username>%s</username>
			<appKey>%s</appKey>
		</authInfo>
	</soap:getTrsSites>`, systemID, rr.password, rr.username, rr.appKey)
	soapRequest := rr.buildSimpleEnvelope(body)

	resp, err := rr.makeRequestSimple(soapRequest)
	if err != nil {
		return nil, err
	}

	// Log the raw XML response for debugging
	log.Printf("=== RAW RADIO REFERENCE SITES XML (first 2000 chars) ===\n%s\n=== END RAW XML ===", string(resp[:min(len(resp), 2000)]))

	var fault SOAPFault
	if err := xml.Unmarshal(resp, &fault); err == nil && fault.FaultCode != "" {
		return nil, fmt.Errorf("SOAP fault: %s - %s", fault.FaultCode, fault.FaultString)
	}

	// Parse the SOAP envelope to get the body content
	bodyContent, err := extractSOAPBody(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to parse SOAP envelope: %v", err)
	}

	// Debug: Print the raw XML response to see the actual structure

	// Use the new site-specific parser instead of the generic one
	sites, err := parseSiteList(bodyContent)
	if err != nil {
		return nil, fmt.Errorf("failed to parse sites: %v", err)
	}

	// Get county names by mapping county IDs
	// First, we need to get the system details to find which state this system belongs to
	system, err := rr.GetSystem(systemID)
	if err != nil {
	} else {
		// Try to get county names if we have state info
		if system.State != "" {
			if stateID, err := strconv.Atoi(system.State); err == nil {
				counties, err := rr.GetCounties(stateID)
				if err == nil {
					// Create a map of county ID to county name
					countyMap := make(map[int]string)
					for _, county := range counties {
						countyMap[county.ID] = county.Name
					}

					// Update sites with county names
					for i := range sites {
						if countyName, exists := countyMap[sites[i].CountyID]; exists {
							sites[i].CountyName = countyName
						} else {
							sites[i].CountyName = fmt.Sprintf("County %d", sites[i].CountyID)
						}
					}

					// Sort sites alphabetically by county name
					sort.Slice(sites, func(i, j int) bool {
						return sites[i].CountyName < sites[j].CountyName
					})

				}
			}
		}
	}

	// Sites processed

	return sites, nil
}

// parseSiteList parses the site list from the Radio Reference API response
func parseSiteList(bodyContent []byte) ([]RadioReferenceSite, error) {
	var sites []RadioReferenceSite

	// Log the body content we're parsing
	log.Printf("=== PARSING SITE LIST - Body Content (first 3000 chars) ===\n%s\n=== END BODY CONTENT ===", string(bodyContent[:min(len(bodyContent), 3000)]))

	// Parse the XML response
	doc, err := xmlquery.Parse(bytes.NewReader(bodyContent))
	if err != nil {
		return nil, fmt.Errorf("failed to parse XML: %v", err)
	}

	// Only TRS site rows have <siteNumber>; nested <item> under <siteFreqs> / <siteLicenses> must be ignored.
	itemNodes := xmlquery.Find(doc, "//item[siteNumber]")
	log.Printf("Found %d site item nodes", len(itemNodes))

	for _, itemNode := range itemNodes {
		site := RadioReferenceSite{}

		// Extract RFSS before siteNumber so composite site IDs include the subsystem
		// (otherwise every site is formatted as "%03d" only and duplicates collide).
		if rfssNode := xmlquery.FindOne(itemNode, "rfss"); rfssNode != nil {
			if rfss, err := strconv.Atoi(rfssNode.InnerText()); err == nil {
				site.RFSS = rfss
			}
		}

		// Extract siteNumber (this is what rdio-scanner needs)
		if numberNode := xmlquery.FindOne(itemNode, "siteNumber"); numberNode != nil {
			if number, err := strconv.Atoi(numberNode.InnerText()); err == nil {
				// Format site ID to include RFSS prefix and 3-digit site number
				if site.RFSS > 0 {
					site.ID = fmt.Sprintf("%d-%03d", site.RFSS, number)
				} else {
					site.ID = fmt.Sprintf("%03d", number)
				}
			}
		}

		// Extract siteDescr
		if descrNode := xmlquery.FindOne(itemNode, "siteDescr"); descrNode != nil {
			site.Name = descrNode.InnerText()
		}

		// Extract latitude
		if latNode := xmlquery.FindOne(itemNode, "lat"); latNode != nil {
			if lat, err := strconv.ParseFloat(latNode.InnerText(), 64); err == nil {
				site.Latitude = lat
			}
		}

		// Extract longitude
		if lonNode := xmlquery.FindOne(itemNode, "lon"); lonNode != nil {
			if lon, err := strconv.ParseFloat(lonNode.InnerText(), 64); err == nil {
				site.Longitude = lon
			}
		}

		// Extract siteCtid (county ID)
		if countyNode := xmlquery.FindOne(itemNode, "siteCtid"); countyNode != nil {
			if countyID, err := strconv.Atoi(countyNode.InnerText()); err == nil {
				site.CountyID = countyID
			}
		}

		// Extract county name
		if countyNameNode := xmlquery.FindOne(itemNode, "countyName"); countyNameNode != nil {
			site.CountyName = countyNameNode.InnerText()
		}

		// Extract frequencies from siteFreqs/item nodes
		// The structure is: <siteFreqs><item><lcn>1</lcn><freq>769.25625</freq>...
		siteFreqsNode := xmlquery.FindOne(itemNode, "siteFreqs")
		if siteFreqsNode != nil {
			freqItems := xmlquery.Find(siteFreqsNode, "item")
			log.Printf("Site %s: Found %d frequency items", site.Name, len(freqItems))

			for _, freqItem := range freqItems {
				// Each item contains lcn, freq, use, colorCode, ch_id
				if freqValueNode := xmlquery.FindOne(freqItem, "freq"); freqValueNode != nil {
					freqText := freqValueNode.InnerText()
					if freq, err := strconv.ParseFloat(freqText, 64); err == nil && freq > 0 {
						site.Frequencies = append(site.Frequencies, freq)
					}
				}
			}
			log.Printf("Site %s: Successfully parsed %d frequencies", site.Name, len(site.Frequencies))
		} else if site.Name != "" {
			log.Printf("Site %s: No siteFreqs node found", site.Name)
		}

		// Only add sites that have at least a number and name
		if site.ID != "" && site.Name != "" {
			log.Printf("Adding site: ID=%s, Name=%s, Frequencies=%d", site.ID, site.Name, len(site.Frequencies))
			sites = append(sites, site)
		}
	}

	log.Printf("Parsed total of %d sites", len(sites))
	return sites, nil
}

func (rr *RadioReferenceService) GetTalkgroups(systemID int) ([]RadioReferenceTalkgroup, error) {
	// Follow SDRTrunk's exact sequence to get system information
	// This approach should work since SDRTrunk successfully gets talkgroup data

	// Step 1: Get system type
	_, err := rr.GetSystemType()
	if err != nil {
		return nil, fmt.Errorf("failed to get system type: %v", err)
	}

	// Step 2: Get system flavor
	_, err = rr.GetSystemFlavor()
	if err != nil {
		return nil, fmt.Errorf("failed to get system flavor: %v", err)
	}

	// Step 3: Get system voice
	_, err = rr.GetSystemVoice()
	if err != nil {
		return nil, fmt.Errorf("failed to get system voice: %v", err)
	}

	// Step 4: Get system tags
	_, err = rr.GetSystemTags()
	if err != nil {
		return nil, fmt.Errorf("failed to get system tags: %v", err)
	}

	// Step 5: Get system details (this should contain talkgroup information)
	_, err = rr.GetSystem(systemID)
	if err != nil {
		return nil, fmt.Errorf("failed to get system details: %v", err)
	}

	// Step 6: Get system sites
	_, err = rr.GetSystemSites(systemID)
	if err != nil {
		return nil, fmt.Errorf("failed to get system sites: %v", err)
	}

	// Now let's try to get ALL talkgroups for the system using the comprehensive method

	allTalkgroups, err := rr.GetAllTalkgroupsForSystem(systemID)
	if err != nil {

		// Fallback to traditional method
		categories, err := rr.GetTalkgroupCategories(systemID)
		if err != nil {
			return []RadioReferenceTalkgroup{}, nil
		} else {

			// Try to get talkgroups from the first category
			if len(categories) > 0 {
				firstCategory := categories[0]

				talkgroups, err := rr.GetTalkgroupsByCategory(systemID, firstCategory.ID, firstCategory.Name)
				if err != nil {
				} else {
					return talkgroups, nil
				}
			}
		}
	} else {
		return allTalkgroups, nil
	}

	return []RadioReferenceTalkgroup{}, nil
}

// GetTalkgroupCategories gets talkgroup categories for a system
func (rr *RadioReferenceService) GetTalkgroupCategories(systemID int) ([]RadioReferenceTalkgroupCategory, error) {
	body := fmt.Sprintf(`<soap:getTrsTalkgroupCats>
	  <sid>%d</sid>
	  <authInfo>
		<style>doc</style>
		<version>18</version>
		<password>%s</password>
		<username>%s</username>
		<appKey>%s</appKey>
	  </authInfo>
	</soap:getTrsTalkgroupCats>`, systemID, rr.password, rr.username, rr.appKey)
	soapRequest := rr.buildSimpleEnvelope(body)

	resp, err := rr.makeRequestSimple(soapRequest)
	if err != nil {
		return nil, err
	}

	var fault SOAPFault
	if err := xml.Unmarshal(resp, &fault); err == nil && fault.FaultCode != "" {
		return nil, fmt.Errorf("SOAP fault: %s - %s", fault.FaultCode, fault.FaultString)
	}

	// Check if response is empty first
	if len(resp) == 0 {
		return []RadioReferenceTalkgroupCategory{}, nil
	}

	// Parse the SOAP envelope to get the body content
	bodyContent, err := extractSOAPBody(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to parse SOAP envelope: %v", err)
	}

	// Use the generic parser that works for countries, states, counties, and systems
	items := parseIdNameList(bodyContent, []string{"tgCid", "id"}, []string{"tgCname", "name", "description"})

	// Convert to RadioReferenceTalkgroupCategory slice
	var categories []RadioReferenceTalkgroupCategory
	for _, item := range items {
		if item.ID > 0 && item.Name != "" {
			categories = append(categories, RadioReferenceTalkgroupCategory{
				ID:          item.ID,
				Name:        item.Name,
				Description: item.Name, // Use name as description for now
			})
		}
	}

	return categories, nil
}

// GetTalkgroupsByCategory gets talkgroups for a specific category in a system
func (rr *RadioReferenceService) GetTalkgroupsByCategory(systemID, categoryID int, categoryName string) ([]RadioReferenceTalkgroup, error) {
	// Try the standard method first
	talkgroups, err := rr.getTalkgroupsByCategoryStandard(systemID, categoryID, categoryName)
	if err == nil && len(talkgroups) > 0 {
		return talkgroups, nil
	}

	// Try alternative parameter combinations
	talkgroups, err = rr.getTalkgroupsByCategoryAlternative(systemID, categoryID, categoryName)
	if err == nil && len(talkgroups) > 0 {
		return talkgroups, nil
	}

	return []RadioReferenceTalkgroup{}, nil
}

// getTalkgroupsByCategoryStandard uses the standard parameter format
func (rr *RadioReferenceService) getTalkgroupsByCategoryStandard(systemID, categoryID int, categoryName string) ([]RadioReferenceTalkgroup, error) {
	body := fmt.Sprintf(`<soap:getTrsTalkgroups>
	  <sid>%d</sid>
	  <tgCid>%d</tgCid>
	  <tgTag></tgTag>
	  <tgDec></tgDec>
	  <authInfo>
		<style>doc</style>
		<version>18</version>
		<password>%s</password>
		<username>%s</username>
		<appKey>%s</appKey>
	  </authInfo>
	</soap:getTrsTalkgroups>`, systemID, categoryID, rr.password, rr.username, rr.appKey)
	soapRequest := rr.buildSimpleEnvelope(body)

	resp, err := rr.makeRequestSimple(soapRequest)
	if err != nil {
		return nil, err
	}

	if len(resp) > 0 {
	}

	var fault SOAPFault
	if err := xml.Unmarshal(resp, &fault); err == nil && fault.FaultCode != "" {
		return nil, fmt.Errorf("SOAP fault: %s - %s", fault.FaultCode, fault.FaultString)
	}

	// Check if response is empty first
	if len(resp) == 0 {
		return []RadioReferenceTalkgroup{}, nil
	}

	// Parse the SOAP envelope to get the body content
	bodyContent, err := extractSOAPBody(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to parse SOAP envelope: %v", err)
	}
	if len(bodyContent) > 0 {
	}

	// Use the generic parser that works for countries, states, counties, and systems
	_ = parseIdNameList(bodyContent, []string{"tgId", "id"}, []string{"tgDescr", "tgAlpha", "description", "name"})

	// Parse the full response structure to get all talkgroup details
	type getTrsTalkgroupsResponse struct {
		Return []struct {
			TgID    int    `xml:"tgId"`
			TgDec   int    `xml:"tgDec"`
			TgDescr string `xml:"tgDescr"`
			TgAlpha string `xml:"tgAlpha"`
			TgMode  string `xml:"tgMode"`
			Enc     int    `xml:"enc"`
			TgCid   int    `xml:"tgCid"`
			TgSort  int    `xml:"tgSort"`
			TgDate  string `xml:"tgDate"`
			Tags    struct {
				Items []struct {
					TagID int `xml:"tagId"`
				} `xml:"item"`
			} `xml:"tags"`
		} `xml:"return>item"`
	}

	var response getTrsTalkgroupsResponse
	if err := xml.Unmarshal(bodyContent, &response); err != nil {
		// Fall back to generic parser results
	} else {
	}

	// Get system tags map to map tag IDs to descriptive names
	systemTagsMap, err := rr.GetSystemTagsMap()
	if err != nil {
		systemTagsMap = make(map[int]string) // Continue with empty tags
	}

	// Convert to RadioReferenceTalkgroup slice
	var talkgroups []RadioReferenceTalkgroup
	if len(response.Return) > 0 {
		// Use detailed parser results
		for _, tg := range response.Return {
			if tg.TgID > 0 {
				// Use tgDescr as description, tgAlpha as alpha tag
				description := tg.TgDescr
				if description == "" {
					description = tg.TgAlpha // Fallback to alpha tag if no description
				}

				// Map tag ID to descriptive tag name
				var tagName string
				if len(tg.Tags.Items) > 0 && len(systemTagsMap) > 0 {
					// Look up tag name directly by tag ID
					if tagNameFromMap, exists := systemTagsMap[tg.Tags.Items[0].TagID]; exists {
						tagName = tagNameFromMap
					}
				}

				talkgroups = append(talkgroups, RadioReferenceTalkgroup{
					ID:          tg.TgDec, // Use tgDec (decimal ID) instead of tgId (internal ID)
					AlphaTag:    tg.TgAlpha,
					Description: description,
					Group:       categoryName,
					Tag:         tagName,
					Enc:         tg.Enc,
				})
			}
		}
	} else {
		// Fall back to enhanced talkgroup parser that can extract all fields
		fallbackTalkgroups := parseTalkgroupsFromXML(bodyContent)

		// Add category information to fallback results
		for i := range fallbackTalkgroups {
			fallbackTalkgroups[i].Group = categoryName
		}

		talkgroups = fallbackTalkgroups
	}

	return talkgroups, nil
}

// getTalkgroupsByCategoryAlternative tries different parameter combinations
func (rr *RadioReferenceService) getTalkgroupsByCategoryAlternative(systemID, categoryID int, categoryName string) ([]RadioReferenceTalkgroup, error) {
	// Try without tgCid parameter - maybe it's not needed
	body := fmt.Sprintf(`<soap:getTrsTalkgroups>
	  <sid>%d</sid>
	  <tgTag></tgTag>
	  <tgDec></tgDec>
	  <authInfo>
		<style>doc</style>
		<version>18</version>
		<password>%s</password>
		<username>%s</username>
		<appKey>%s</appKey>
	  </authInfo>
	</soap:getTrsTalkgroups>`, systemID, rr.password, rr.username, rr.appKey)
	soapRequest := rr.buildSimpleEnvelope(body)

	resp, err := rr.makeRequestSimple(soapRequest)
	if err != nil {
		return nil, err
	}

	if len(resp) > 0 {
	}

	var fault SOAPFault
	if err := xml.Unmarshal(resp, &fault); err == nil && fault.FaultCode != "" {
		return nil, fmt.Errorf("SOAP fault: %s - %s", fault.FaultCode, fault.FaultString)
	}

	// Check if response is empty first
	if len(resp) == 0 {
		return []RadioReferenceTalkgroup{}, nil
	}

	// Parse the SOAP envelope to get the body content
	bodyContent, err := extractSOAPBody(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to parse SOAP envelope: %v", err)
	}

	// Use the generic parser that works for countries, states, counties, and systems
	_ = parseIdNameList(bodyContent, []string{"tgId", "id"}, []string{"tgDescr", "tgAlpha", "description", "name"})

	// Parse the full response structure to get all talkgroup details
	type getTrsTalkgroupsResponse struct {
		Return []struct {
			TgID    int    `xml:"tgId"`
			TgDec   int    `xml:"tgDec"`
			TgDescr string `xml:"tgDescr"`
			TgAlpha string `xml:"tgAlpha"`
			TgMode  string `xml:"tgMode"`
			Enc     int    `xml:"enc"`
			TgCid   int    `xml:"tgCid"`
			TgSort  int    `xml:"tgSort"`
			TgDate  string `xml:"tgDate"`
			Tags    struct {
				Items []struct {
					TagID int `xml:"tagId"`
				} `xml:"item"`
			} `xml:"tags"`
		} `xml:"return>item"`
	}

	var response getTrsTalkgroupsResponse
	if err := xml.Unmarshal(bodyContent, &response); err != nil {
		// Fall back to generic parser results
	} else {
	}

	// Get system tags map to map tag IDs to descriptive names
	systemTagsMap, err := rr.GetSystemTagsMap()
	if err != nil {
		systemTagsMap = make(map[int]string) // Continue with empty tags
	}

	// Convert to RadioReferenceTalkgroup slice
	var talkgroups []RadioReferenceTalkgroup
	if len(response.Return) > 0 {
		// Use detailed parser results
		for _, tg := range response.Return {
			if tg.TgID > 0 {
				// Use tgDescr as description, tgAlpha as alpha tag
				description := tg.TgDescr
				if description == "" {
					description = tg.TgAlpha // Fallback to alpha tag if no description
				}

				// Map tag ID to descriptive tag name
				var tagName string
				if len(tg.Tags.Items) > 0 && len(systemTagsMap) > 0 {
					// Look up tag name directly by tag ID
					if tagNameFromMap, exists := systemTagsMap[tg.Tags.Items[0].TagID]; exists {
						tagName = tagNameFromMap
					}
				}

				talkgroups = append(talkgroups, RadioReferenceTalkgroup{
					ID:          tg.TgDec, // Use tgDec (decimal ID) instead of tgId (internal ID)
					AlphaTag:    tg.TgAlpha,
					Description: description,
					Group:       categoryName,
					Tag:         tagName,
					Enc:         tg.Enc,
				})
			}
		}
	} else {
		// Fall back to enhanced talkgroup parser that can extract all fields
		fallbackTalkgroups := parseTalkgroupsFromXML(bodyContent)

		// Add category information to fallback results
		for i := range fallbackTalkgroups {
			fallbackTalkgroups[i].Group = categoryName
		}

		talkgroups = fallbackTalkgroups
	}

	return talkgroups, nil
}

// Helper function for min
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// GetAllTalkgroupsByCategories gets all talkgroups for a system by iterating through categories
// This is a more reliable approach than trying to get all talkgroups at once
func (rr *RadioReferenceService) GetAllTalkgroupsByCategories(systemID int) ([]RadioReferenceTalkgroup, error) {

	// First, get all talkgroup categories for this system
	categories, err := rr.GetTalkgroupCategories(systemID)
	if err != nil {
		return nil, fmt.Errorf("failed to get talkgroup categories: %v", err)
	}

	if len(categories) == 0 {
		return []RadioReferenceTalkgroup{}, nil
	}

	var allTalkgroups []RadioReferenceTalkgroup

	// Iterate through each category and get talkgroups
	for _, category := range categories {

		talkgroups, err := rr.GetTalkgroupsByCategory(systemID, category.ID, category.Name)
		if err != nil {
			// Continue with other categories instead of failing completely
			continue
		}

		// Add category information to each talkgroup
		for range talkgroups {
			// We could extend the RadioReferenceTalkgroup struct to include category info
			// For now, we'll just add them to the list
		}

		allTalkgroups = append(allTalkgroups, talkgroups...)
	}

	return allTalkgroups, nil
}

// GetTalkgroupsOrganizedByCategory gets talkgroups organized by category for a system
// This gives users a better way to browse talkgroups by agency/function
func (rr *RadioReferenceService) GetTalkgroupsOrganizedByCategory(systemID int) (map[string][]RadioReferenceTalkgroup, error) {

	// First, get all talkgroup categories for this system
	categories, err := rr.GetTalkgroupCategories(systemID)
	if err != nil {
		return nil, fmt.Errorf("failed to get talkgroup categories: %v", err)
	}

	if len(categories) == 0 {
		return make(map[string][]RadioReferenceTalkgroup), nil
	}

	organizedTalkgroups := make(map[string][]RadioReferenceTalkgroup)

	// Iterate through each category and get talkgroups
	for _, category := range categories {

		talkgroups, err := rr.GetTalkgroupsByCategory(systemID, category.ID, category.Name)
		if err != nil {
			// Continue with other categories instead of failing completely
			continue
		}

		// Use category name as the key for organization
		categoryKey := category.Name
		if categoryKey == "" {
			categoryKey = fmt.Sprintf("Category %d", category.ID)
		}

		organizedTalkgroups[categoryKey] = talkgroups
	}

	return organizedTalkgroups, nil
}

// Alternative method to get talkgroups using the working import approach
func (rr *RadioReferenceService) GetTalkgroupsAlternative(systemID int) ([]RadioReferenceTalkgroup, error) {

	// Try to use the same approach as the working import method
	// This might use a different API endpoint or method

	// For now, let's try to get the system first to see what information we have
	_, err := rr.GetSystem(systemID)
	if err != nil {
		return nil, fmt.Errorf("failed to get system details: %v", err)
	}

	// Try to use the same SOAP method that the working import uses
	// The issue might be in the SOAP envelope format
	body := fmt.Sprintf(`<soap:getTrsTalkgroups>
      <sid>%d</sid>
      <authInfo>
        <style>doc</style>
        <version>18</version>
        <password>%s</password>
        <username>%s</username>
        <appKey>%s</appKey>
      </authInfo>
    </soap:getTrsTalkgroups>`, systemID, rr.password, rr.username, rr.appKey)

	// Use the same envelope building method
	soapRequest := rr.buildSimpleEnvelope(body)

	// Try with SOAPAction header first
	resp, err := rr.makeRequestWithAction("getTrsTalkgroups", soapRequest)
	if err != nil {
		// Fallback to simple request
		resp, err = rr.makeRequestSimple(soapRequest)
		if err != nil {
			return nil, fmt.Errorf("alternative method request failed: %v", err)
		}
	}

	// Debug: Log the response
	respStr := string(resp)
	if len(respStr) > 200 {
	} else {
	}

	// Try to parse the response
	var fault SOAPFault
	if err := xml.Unmarshal(resp, &fault); err == nil && fault.FaultCode != "" {
		return []RadioReferenceTalkgroup{}, nil
	}

	// Check if response is empty first
	if len(resp) == 0 {
		return []RadioReferenceTalkgroup{}, nil
	}

	// Parse the SOAP envelope to get the body content
	bodyContent, err := extractSOAPBody(resp)
	if err != nil {
		return []RadioReferenceTalkgroup{}, nil
	}

	var talkgroups []RadioReferenceTalkgroup
	if err := xml.Unmarshal(bodyContent, &talkgroups); err != nil {
		return []RadioReferenceTalkgroup{}, nil
	}

	return talkgroups, nil
}

func (rr *RadioReferenceService) GetSites(systemID int) ([]RadioReferenceSite, error) {
	// Use the new GetSystemSites method that follows SDRTrunk's format
	return rr.GetSystemSites(systemID)
}

func (rr *RadioReferenceService) GetFrequencies(subCategoryID int) ([]RadioReferenceFrequency, error) {
	body := fmt.Sprintf(`<soap:getSubCategoryFrequencies>
      <request>%d</request>
      <authInfo>
        <style>doc</style>
        <version>18</version>
        <password>%s</password>
        <username>%s</username>
        <appKey>%s</appKey>
      </authInfo>
    </soap:getSubCategoryFrequencies>`, subCategoryID, rr.password, rr.username, rr.appKey)
	soapRequest := rr.buildSimpleEnvelope(body)

	resp, err := rr.makeRequestSimple(soapRequest)
	if err != nil {
		return nil, err
	}

	var fault SOAPFault
	if err := xml.Unmarshal(resp, &fault); err == nil && fault.FaultCode != "" {
		return nil, fmt.Errorf("SOAP fault: %s - %s", fault.FaultCode, fault.FaultString)
	}

	// Parse the SOAP envelope to get the body content
	bodyContent, err := extractSOAPBody(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to parse SOAP envelope: %v", err)
	}

	var frequencies []RadioReferenceFrequency
	if err := xml.Unmarshal(bodyContent, &frequencies); err != nil {
		return nil, fmt.Errorf("failed to parse frequencies: %v", err)
	}

	return frequencies, nil
}

func (rr *RadioReferenceService) SearchSystems(query string) ([]RadioReferenceSystem, error) {
	body := fmt.Sprintf(`<soap:searchSystems>
      <query>%s</query>
      <authInfo>
        <style>doc</style>
        <version>18</version>
        <password>%s</password>
        <username>%s</username>
        <appKey>%s</appKey>
      </authInfo>
    </soap:searchSystems>`, query, rr.password, rr.username, rr.appKey)
	soapRequest := rr.buildSimpleEnvelope(body)

	resp, err := rr.makeRequestSimple(soapRequest)
	if err != nil {
		return nil, err
	}

	var fault SOAPFault
	if err := xml.Unmarshal(resp, &fault); err == nil && fault.FaultCode != "" {
		return nil, fmt.Errorf("SOAP fault: %s - %s", fault.FaultCode, fault.FaultString)
	}

	// Parse the SOAP envelope to get the body content
	bodyContent, err := extractSOAPBody(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to parse SOAP envelope: %v", err)
	}

	var systems []RadioReferenceSystem
	if err := xml.Unmarshal(bodyContent, &systems); err != nil {
		return nil, fmt.Errorf("failed to parse systems: %v", err)
	}

	return systems, nil
}

func (rr *RadioReferenceService) makeRequest(soapAction string, soapRequest string) ([]byte, error) {
	req, err := http.NewRequest("POST", RADIO_REFERENCE_BASE_URL, strings.NewReader(soapRequest))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	// SOAP 1.1 headers
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	req.Header.Set("SOAPAction", soapAction)
	req.Header.Set("User-Agent", "thinline-radio/1.0")
	req.Header.Set("Content-Length", fmt.Sprintf("%d", len(soapRequest)))

	resp, err := rr.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %v", err)
	}
	defer resp.Body.Close()

	// Accept both 200 (OK) and 500 (Internal Server Error) as Radio Reference sometimes returns 500 for valid responses
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusInternalServerError {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %v", err)
	}

	// Debug preview
	preview := string(body)
	if len(preview) > 500 {
		preview = preview[:500] + "..."
	}

	return body, nil
}

// buildSimpleEnvelope constructs a proper SOAP envelope with correct namespaces matching Radio Reference API
func (rr *RadioReferenceService) buildSimpleEnvelope(bodyInner string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:xsd="http://www.w3.org/2001/XMLSchema">
  <soap:Body>
    %s
  </soap:Body>
</soap:Envelope>`, bodyInner)
}

// makeRequestSimple posts a SOAP 1.1 request without a SOAPAction header and with a strict content-type
// of text/xml;charset=UTF-8 to match the Java client behavior.
func (rr *RadioReferenceService) makeRequestSimple(soapRequest string) ([]byte, error) {
	req, err := http.NewRequest("POST", RADIO_REFERENCE_BASE_URL, strings.NewReader(soapRequest))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	// Match Java client headers
	req.Header.Set("Content-Type", "text/xml;charset=UTF-8")
	req.Header.Set("User-Agent", "io.github.dsheirer.rrapi")

	resp, err := rr.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusInternalServerError {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %v", err)
	}

	// Check if response is empty
	if len(body) == 0 {
		return body, nil
	}

	preview := string(body)
	if len(preview) > 500 {
		preview = preview[:500] + "..."
	}

	return body, nil
}

// makeRequestWithAction posts a SOAP 1.1 request with a SOAPAction header, for methods that may require it
func (rr *RadioReferenceService) makeRequestWithAction(soapAction string, soapRequest string) ([]byte, error) {
	req, err := http.NewRequest("POST", RADIO_REFERENCE_BASE_URL, strings.NewReader(soapRequest))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "text/xml;charset=UTF-8")
	req.Header.Set("User-Agent", "io.github.dsheirer.rrapi")
	req.Header.Set("SOAPAction", soapAction)

	resp, err := rr.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusInternalServerError {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %v", err)
	}

	// Check if response is empty
	if len(body) == 0 {
		return body, nil
	}

	preview := string(body)
	if len(preview) > 500 {
		preview = preview[:500] + "..."
	}

	return body, nil
}

// GetAllTalkgroupsForSystem gets all talkgroups for a system by iterating through all categories
// This gives us all talkgroups for the county/system
func (rr *RadioReferenceService) GetAllTalkgroupsForSystem(systemID int) ([]RadioReferenceTalkgroup, error) {

	// First, get all talkgroup categories for this system
	categories, err := rr.GetTalkgroupCategories(systemID)
	if err != nil {
		return nil, fmt.Errorf("failed to get talkgroup categories: %v", err)
	}

	if len(categories) == 0 {
		return []RadioReferenceTalkgroup{}, nil
	}

	var allTalkgroups []RadioReferenceTalkgroup

	// Iterate through each category and get talkgroups
	for _, category := range categories {

		talkgroups, err := rr.GetTalkgroupsByCategory(systemID, category.ID, category.Name)
		if err != nil {
			// Continue with other categories instead of failing completely
			continue
		}

		// Add category information to each talkgroup
		for j := range talkgroups {
			if talkgroups[j].Group == "" {
				talkgroups[j].Group = category.Name
			}
		}

		allTalkgroups = append(allTalkgroups, talkgroups...)
	}

	return allTalkgroups, nil
}
