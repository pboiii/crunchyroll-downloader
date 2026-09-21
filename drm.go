package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/Eyevinn/mp4ff/mp4"
	"github.com/iyear/gowidevine"
	"github.com/iyear/gowidevine/widevinepb"
	"github.com/unki2aut/go-mpd"
)

var keys []*widevine.Key

func contentKeyForTrack(data []byte, licenseKeys []*widevine.Key) ([]byte, error) {
	reader := bytes.NewReader(data)
	var moov *mp4.MoovBox
	for reader.Len() > 0 {
		box, err := mp4.DecodeBox(uint64(len(data)-reader.Len()), reader)
		if err != nil {
			return nil, fmt.Errorf("read track initialization: %w", err)
		}
		moov, _ = box.(*mp4.MoovBox)
		if moov != nil {
			break
		}
	}
	if moov == nil {
		return nil, errors.New("track initialization not found")
	}
	if len(moov.Traks) != 1 {
		return nil, errors.New("expected one media track per provider representation")
	}
	protection := moov.GetSinf(moov.Traks[0].Tkhd.TrackID)
	if protection == nil || protection.Schi == nil || protection.Schi.Tenc == nil {
		return nil, errors.New("track has no encryption key identifier")
	}
	for _, key := range licenseKeys {
		if key != nil && key.Type == widevinepb.License_KeyContainer_CONTENT && bytes.Equal(key.ID, protection.Schi.Tenc.DefaultKID) {
			return key.Key, nil
		}
	}
	return nil, errors.New("license has no content key matching this media track")
}

func getPssh(mpd *mpd.MPD) *string {
	if mpd == nil || len(mpd.Period) == 0 || mpd.Period[0] == nil || len(mpd.Period[0].AdaptationSets) == 0 {
		return nil
	}
	set := mpd.Period[0].AdaptationSets[0]
	if set == nil {
		return nil
	}

	for _, contentProtection := range set.ContentProtections {
		if contentProtection.SchemeIDURI == nil || !strings.EqualFold(*contentProtection.SchemeIDURI, "urn:uuid:edef8ba9-79d6-4ace-a3c8-27dcd51d21ed") {
			continue
		}
		if contentProtection.CencPSSH == nil || strings.TrimSpace(*contentProtection.CencPSSH) == "" {
			continue
		}
		return contentProtection.CencPSSH
	}

	return nil
}

type CrunchyrollWidevineLicenseResponse struct {
	License string `json:"license"`
}

func sendChallenge(contentId, videoToken string, challenge []byte) ([]byte, error) {
	req, err := http.NewRequest(http.MethodPost, "https://www.crunchyroll.com/license/v1/license/widevine", io.NopCloser(bytes.NewReader(challenge)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Cr-Content-Id", contentId)
	req.Header.Set("X-Cr-Video-Token", videoToken)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Origin", "https://static.crunchyroll.com")
	req.Header.Set("Referer", "https://static.crunchyroll.com/")
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:147.0) Gecko/20100101 Firefox/147.0")
	resp, err := DoRequest(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Parse JSON response
	res, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var result CrunchyrollWidevineLicenseResponse
	if err = json.Unmarshal(res, &result); err != nil {
		return nil, err
	}

	decoded, err := base64.StdEncoding.DecodeString(result.License)
	if err != nil {
		return nil, err
	}

	return decoded, nil
}

func getWidevineDevice() (*widevine.Device, error) {
	wvdPath := strings.TrimSpace(os.Getenv("CRUNCHYROLL_WIDEVINE_DEVICE_FILE"))
	if wvdPath != "" {
		wvd, err := openPrivateRegularFile(wvdPath)
		if err != nil {
			return nil, fmt.Errorf("open Widevine device file: %w", err)
		}
		defer wvd.Close()
		return widevine.NewDevice(widevine.FromWVD(io.NopCloser(wvd)))
	}

	clientIDPath := strings.TrimSpace(os.Getenv("CRUNCHYROLL_WIDEVINE_CLIENT_ID_FILE"))
	privateKeyPath := strings.TrimSpace(os.Getenv("CRUNCHYROLL_WIDEVINE_PRIVATE_KEY_FILE"))
	if clientIDPath == "" && privateKeyPath == "" {
		return nil, nil
	}
	if clientIDPath == "" || privateKeyPath == "" {
		return nil, fmt.Errorf("both CRUNCHYROLL_WIDEVINE_CLIENT_ID_FILE and CRUNCHYROLL_WIDEVINE_PRIVATE_KEY_FILE are required")
	}
	clientIDFile, err := openPrivateRegularFile(clientIDPath)
	if err != nil {
		return nil, fmt.Errorf("open Widevine client id file: %w", err)
	}
	defer clientIDFile.Close()
	privateKeyFile, err := openPrivateRegularFile(privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("open Widevine private key file: %w", err)
	}
	defer privateKeyFile.Close()
	clientID, err := io.ReadAll(clientIDFile)
	if err != nil {
		return nil, fmt.Errorf("read Widevine client id file: %w", err)
	}
	privateKey, err := io.ReadAll(privateKeyFile)
	if err != nil {
		return nil, fmt.Errorf("read Widevine private key file: %w", err)
	}

	if len(clientID) > 0 && len(privateKey) > 0 {
		return widevine.NewDevice(widevine.FromRaw(clientID, privateKey))
	}

	return nil, nil
}

func openPrivateRegularFile(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("credential path must be a regular file")
	}
	if info.Mode().Perm() != 0o600 {
		return nil, fmt.Errorf("credential file mode must be 0600")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	opened, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if !os.SameFile(info, opened) {
		file.Close()
		return nil, fmt.Errorf("credential file changed while opening")
	}
	return file, nil
}

func getLicense(psshData, contentId, videoToken string) error {
	device, err := getWidevineDevice()
	if err != nil {
		return err
	}
	if device == nil {
		return errors.New("no authorized Widevine device configured; set CRUNCHYROLL_WIDEVINE_DEVICE_FILE or both private raw-device file variables for full-media downloads (subtitle indexing requires none)")
	}
	cdm := widevine.NewCDM(device)
	decodedPssh, err := base64.StdEncoding.DecodeString(psshData)
	if err != nil {
		return err
	}
	pssh, err := widevine.NewPSSH(decodedPssh)
	if err != nil {
		return err
	}

	challenge, parseLicense, err := cdm.GetLicenseChallenge(pssh, widevinepb.LicenseType_AUTOMATIC, false)
	if err != nil {
		return err
	}
	resp, err := sendChallenge(contentId, videoToken, challenge)
	if err != nil {
		return err
	}
	keys, err = parseLicense(resp)
	if err != nil {
		return err
	}

	return nil
}
