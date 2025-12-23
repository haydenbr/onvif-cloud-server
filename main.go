package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	localHTTPPort           = 8081
	gatewayHTTPPort         = 18809
	rtspHost                = "4.tcp.ngrok.io"
	rtspPort                = 10512
	soapNamespace           = "http://www.w3.org/2003/05/soap-envelope"
	tdsNamespace            = "http://www.onvif.org/ver10/device/wsdl"
	tr2Namespace            = "http://www.onvif.org/ver20/media/wsdl"
	ttNamespace             = "http://www.onvif.org/ver10/schema"
	tmdNamespace            = "http://www.onvif.org/ver10/deviceIO/wsdl"
	soapContentType         = "application/soap+xml; charset=utf-8"
	videoSourceToken        = "video_source"
	videoSourceConfigToken  = "video_source_config1"
	mediaProfileToken       = "media_profile1"
	videoEncoderConfigToken = "video_encoder_config"
	terNamespace            = "http://www.onvif.org/ver10/error"
)

var rtspURL = fmt.Sprintf("rtsp://%s:%d/test", rtspHost, rtspPort)

var appLogger = slog.New(slog.NewTextHandler(os.Stdout, nil))

func main() {
	gin.SetMode(gin.ReleaseMode)

	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(requestLogger())
	router.POST("/onvif/device_service", deviceServiceHandler())
	router.POST("/onvif/deviceio_service", deviceIOServiceHandler())
	router.POST("/onvif/media2_service", media2ServiceHandler())

	router.NoRoute(func(c *gin.Context) {
		appLogger.Warn("NoRoute hit", "method", c.Request.Method, "path", c.Request.URL.Path)
		payload := buildServiceNotSupportedFault(c.Request.URL.Path)
		c.Data(http.StatusBadRequest, soapContentType, []byte(payload))
	})

	addr := ":" + strconv.Itoa(localHTTPPort)
	appLogger.Info("HTTP request logger listening", "addr", addr)

	if err := router.Run(addr); err != nil {
		appLogger.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

// requestLogger logs request/response metadata and bodies for quick inspection.
func requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		var body []byte
		if c.Request.Body != nil {
			body, _ = io.ReadAll(c.Request.Body)
			c.Request.Body = io.NopCloser(bytes.NewBuffer(body))
		}

		blw := &bodyLogWriter{ResponseWriter: c.Writer}
		c.Writer = blw

		c.Next()

		appLogger.Info(
			"request completed",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"client", c.ClientIP(),
			"query", c.Request.URL.RawQuery,
			"headers", c.Request.Header,
			"request_body", string(body),
			"status", c.Writer.Status(),
			"response_size", c.Writer.Size(),
			"response_body", blw.body.String(),
		)
	}
}

type soapEnvelope struct {
	Body struct {
		Raw string `xml:",innerxml"`
	} `xml:"Body"`
}

type getStreamUriRequest struct {
	XMLName      xml.Name    `xml:"GetStreamUri"`
	StreamSetup  streamSetup `xml:"StreamSetup"`
	ProfileToken string      `xml:"ProfileToken"`
}

type streamSetup struct {
	Stream    string    `xml:"Stream"`
	Transport transport `xml:"Transport"`
}

type transport struct {
	Protocol string `xml:"Protocol"`
}

type RTSPProtocol = string

const (
	RTSPProtocolUnicast    RTSPProtocol = "RtspUnicast"
	RTSPProtocolMulticast  RTSPProtocol = "RtspMulticast"
	RTSPProtocolsUnicast   RTSPProtocol = "RtspsUnicast"
	RTSPProtocolsMulticast RTSPProtocol = "RtspsMulticast"
	RTSPProtocolRTSP       RTSPProtocol = "RTSP"
	RTSPProtocolOverHttp   RTSPProtocol = "RtspOverHttp"
)

type media2GetStreamUriRequest struct {
	XMLName      xml.Name     `xml:"GetStreamUri"`
	Protocol     RTSPProtocol `xml:"Protocol"`
	ProfileToken string       `xml:"ProfileToken"`
}

type bodyLogWriter struct {
	gin.ResponseWriter
	body bytes.Buffer
}

func (w *bodyLogWriter) Write(b []byte) (int, error) {
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

func (w *bodyLogWriter) WriteString(s string) (int, error) {
	w.body.WriteString(s)
	return w.ResponseWriter.WriteString(s)
}

func parseMedia2GetStreamUriRequest(raw string) (media2GetStreamUriRequest, error) {
	var req media2GetStreamUriRequest
	if err := xml.Unmarshal([]byte(raw), &req); err != nil {
		return req, err
	}
	return req, nil
}

func deviceServiceHandler() gin.HandlerFunc {
	const (
		getServicesAction          = "GetServices"
		getNetworkInterfacesAction = "GetNetworkInterfaces"
		getDeviceInfoAction        = "GetDeviceInformation"
		getNetworkProtocolsAction  = "GetNetworkProtocols"
		getUsersAction             = "GetUsers"
	)

	return func(c *gin.Context) {
		var envelope soapEnvelope
		if err := xml.NewDecoder(c.Request.Body).Decode(&envelope); err != nil {
			appLogger.Warn("failed to parse device request", "err", err)
			c.Status(http.StatusBadRequest)
			return
		}

		scheme := requestScheme(c)
		host := c.Request.Host
		if host == "" {
			panic("no host!!!")
		}

		bodyContent := strings.TrimSpace(envelope.Body.Raw)
		switch {
		case strings.Contains(bodyContent, getServicesAction):
			payload := buildGetServicesResponse(scheme, host)
			c.Data(http.StatusOK, soapContentType, []byte(payload))
		case strings.Contains(bodyContent, getNetworkInterfacesAction):
			payload := buildGetNetworkInterfacesResponse()
			c.Data(http.StatusOK, soapContentType, []byte(payload))
		case strings.Contains(bodyContent, getDeviceInfoAction):
			payload := buildGetDeviceInformationResponse()
			c.Data(http.StatusOK, soapContentType, []byte(payload))
		case strings.Contains(bodyContent, getNetworkProtocolsAction):
			payload := buildGetNetworkProtocolsResponse()
			c.Data(http.StatusOK, soapContentType, []byte(payload))
		case strings.Contains(bodyContent, getUsersAction):
			payload := buildGetUsersResponse()
			c.Data(http.StatusOK, soapContentType, []byte(payload))
		default:
			action := detectSOAPAction(bodyContent)
			appLogger.Warn("device_service request action not recognized", "action", action, "body", bodyContent)
			payload := buildActionNotSupportedFault(action)
			c.Data(http.StatusBadRequest, soapContentType, []byte(payload))
		}
	}
}

func buildGetServicesResponse(scheme, host string) string {
	deviceAddress := fmt.Sprintf("%s://%s/onvif/device_service", scheme, host)
	media2Address := fmt.Sprintf("%s://%s/onvif/media2_service", scheme, host)
	deviceIOAddress := fmt.Sprintf("%s://%s/onvif/deviceio_service", scheme, host)

	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="%s"
	xmlns:tds="%s"
	xmlns:tt="%s"
	xmlns:tr2="%s">
	<s:Body>
		<tds:GetServicesResponse>
			<tds:Service>
				<tds:Namespace>%s</tds:Namespace>
				<tds:XAddr>%s</tds:XAddr>
				<tds:Version>
					<tt:Major>25</tt:Major>
					<tt:Minor>06</tt:Minor>
				</tds:Version>
				<tds:Capabilities>
					<tds:Capabilities>
						<tds:Network IPFilter="false" ZeroConfiguration="false" IPVersion6="false" DynDNS="false" Dot11Configuration="false" Dot1XConfigurations="0" HostnameFromDHCP="false" NTP="0" DHCPv6="false" />
						<tds:Security TLS1.0="false" TLS1.1="false" TLS1.2="false" OnboardKeyGeneration="false" AccessPolicyConfig="false" DefaultAccessPolicy="false" Dot1X="false" RemoteUserHandling="false" X.509Token="false" SAMLToken="false" KerberosToken="false" UsernameToken="false" HttpDigest="true" RELToken="false" JsonWebToken="false" SupportedEAPMethods="" MaxUsers="1" MaxUserNameLength="0" MaxPasswordLength="0" SecurityPolicies="" MaxPasswordHistory="0" HashingAlgorithms="MD5,SHA-256" />
						<tds:System DiscoveryResolve="false" DiscoveryBye="false" RemoteDiscovery="true" SystemBackup="false" SystemLogging="false" FirmwareUpgrade="false" HttpFirmwareUpgrade="false" HttpSystemBackup="false" HttpSystemLogging="false" HttpSupportInformation="false" StorageConfiguration="false" MaxStorageConfigurations="0" StorageConfigurationRenewal="false" GeoLocationEntries="1" AutoGeo="" StorageTypesSupported="" DiscoveryNotSupported="true" NetworkConfigNotSupported="true" UserConfigNotSupported="true" Addons="" HardwareType="Camera" />
						<tds:Misc AuxiliaryCommands="" />
					</tds:Capabilities>
				</tds:Capabilities>
			</tds:Service>
			<tds:Service>
				<tds:Namespace>%s</tds:Namespace>
				<tds:XAddr>%s</tds:XAddr>
				<tds:Version>
					<tt:Major>25</tt:Major>
					<tt:Minor>06</tt:Minor>
				</tds:Version>
				<tds:Capabilities>
					<tr2:Capabilities SnapshotUri="false" Rotation="false" VideoSourceMode="false" OSD="false" TemporaryOSDText="false" Mask="false" SourceMask="false" WebRTC="0">
						<tr2:ProfileCapabilities MaximumNumberOfProfiles="2" ConfigurationsSupported="VideoSource VideoEncoder" />
						<tr2:StreamingCapabilities RTSPStreaming="true" SecureRTSPStreaming="true" RTPMulticast="false" RTP_RTSP_TCP="true" NonAggregateControl="false" RTSPWebSocketUri="" AutoStartMulticast="false" />
						<tr2:MediaSigningCapabilities MediaSigningSupported="false" />
						<tr2:AudioClipCapabilities MaxAudioClipLimit="0" MaxAudioClipSize="0" SupportedAudioClipFormat="" />
					</tr2:Capabilities>
				</tds:Capabilities>
			</tds:Service>
			<tds:Service>
				<tds:Namespace>%s</tds:Namespace>
				<tds:XAddr>%s</tds:XAddr>
				<tds:Capabilities>
					<tmd:Capabilities VideoSources="1" VideoOutputs="0" AudioSources="0" AudioOutputs="0" RelayOutputs="0" DigitalInputs="0" SerialPorts="0"></tmd:Capabilities>
				</tds:Capabilities>
				<tds:Version>
					<tt:Major>25</tt:Major>
					<tt:Minor>06</tt:Minor>
				</tds:Version>
			</tds:Service>
		</tds:GetServicesResponse>
	</s:Body>
	</s:Envelope>`,
		soapNamespace,
		tdsNamespace,
		ttNamespace,
		tr2Namespace,
		tdsNamespace,
		deviceAddress,
		tr2Namespace,
		media2Address,
		tmdNamespace,
		deviceIOAddress,
	)
}

func buildGetNetworkInterfacesResponse() string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope
	xmlns:s="%s"
	xmlns:tds="%s"
	xmlns:tt="%s">
	<s:Body>
		<tds:GetNetworkInterfacesResponse>
			<tds:NetworkInterfaces token="eth0">
				<tt:Enabled>true</tt:Enabled>
        <tt:Info>
          <tt:Name>eth0</tt:Name>
          <tt:HwAddress>02:01:23:45:67:89</tt:HwAddress>
          <tt:MTU>1500</tt:MTU>
        </tt:Info>
        <tt:IPv4>
          <tt:Enabled>true</tt:Enabled>
          <tt:Config>
            <tt:Manual>
              <tt:Address>0.0.0.0</tt:Address>
              <tt:PrefixLength>24</tt:PrefixLength>
            </tt:Manual>
            <tt:DHCP>false</tt:DHCP>
          </tt:Config>
        </tt:IPv4>
			</tds:NetworkInterfaces>
		</tds:GetNetworkInterfacesResponse>
	</s:Body>
</s:Envelope>`, soapNamespace, tdsNamespace, ttNamespace)
}

func buildGetDeviceInformationResponse() string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="%s"
	xmlns:tds="%s"
	xmlns:tt="%s">
	<s:Body>
		<tds:GetDeviceInformationResponse>
			<tds:Manufacturer>Cool Camera Co.</tds:Manufacturer>
			<tds:Model>Cool Camera</tds:Model>
			<tds:FirmwareVersion>v1.0</tds:FirmwareVersion>
			<tds:SerialNumber>serialnumber123</tds:SerialNumber>
			<tds:HardwareId>hardwareid123</tds:HardwareId>
		</tds:GetDeviceInformationResponse>
	</s:Body>
</s:Envelope>`, soapNamespace, tdsNamespace, ttNamespace)
}

func buildGetUsersResponse() string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="%s"
	xmlns:tds="%s"
	xmlns:tt="%s">
	<s:Body>
		<tds:GetUsersResponse>
			<tds:User>
				<tt:Username>user</tt:Username>
				<tt:UserLevel>User</tt:UserLevel>
			</tds:User>
		</tds:GetUsersResponse>
	</s:Body>
</s:Envelope>`, soapNamespace, tdsNamespace, ttNamespace)
}

func buildGetNetworkProtocolsResponse() string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="%s"
	xmlns:tds="%s"
	xmlns:tt="%s">
	<s:Body>
		<tds:GetNetworkProtocolsResponse>
			<tds:NetworkProtocols>
				<tt:Name>HTTP</tt:Name>
				<tt:Enabled>true</tt:Enabled>
				<tt:Port>%d</tt:Port>
			</tds:NetworkProtocols>
			<tds:NetworkProtocols>
				<tt:Name>HTTPS</tt:Name>
				<tt:Enabled>false</tt:Enabled>
				<tt:Port>443</tt:Port>
			</tds:NetworkProtocols>
			<tds:NetworkProtocols>
				<tt:Name>RTSP</tt:Name>
				<tt:Enabled>true</tt:Enabled>
				<tt:Port>%d</tt:Port>
			</tds:NetworkProtocols>
		</tds:GetNetworkProtocolsResponse>
	</s:Body>
</s:Envelope>`, soapNamespace, tdsNamespace, ttNamespace, gatewayHTTPPort, rtspPort)
}

func requestScheme(c *gin.Context) string {
	if proto := c.GetHeader("X-Forwarded-Proto"); proto != "" {
		return proto
	}

	if c.Request.TLS != nil {
		return "https"
	}

	if c.Request.URL != nil && c.Request.URL.Scheme != "" {
		return c.Request.URL.Scheme
	}

	return "http"
}

func deviceIOServiceHandler() gin.HandlerFunc {
	const getVideoSourcesAction = "GetVideoSources"

	return func(c *gin.Context) {
		var envelope soapEnvelope
		if err := xml.NewDecoder(c.Request.Body).Decode(&envelope); err != nil {
			appLogger.Warn("failed to parse deviceio request", "err", err)
			c.Status(http.StatusBadRequest)
			return
		}

		bodyContent := strings.TrimSpace(envelope.Body.Raw)
		switch {
		case strings.Contains(bodyContent, getVideoSourcesAction):
			payload := buildDeviceIOGetVideoSourcesResponse()
			c.Data(http.StatusOK, soapContentType, []byte(payload))
		default:
			action := detectSOAPAction(bodyContent)
			appLogger.Warn("deviceio request action not recognized", "action", action, "body", bodyContent)
			payload := buildActionNotSupportedFault(action)
			c.Data(http.StatusBadRequest, soapContentType, []byte(payload))
		}
	}
}

func buildDeviceIOGetVideoSourcesResponse() string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="%s"
	xmlns:tmd="%s"
	xmlns:tt="%s">
	<s:Body>
		<tmd:GetVideoSourcesResponse>
			<tmd:Token>%s</tmd:Token>
		</tmd:GetVideoSourcesResponse>
	</s:Body>
</s:Envelope>`, soapNamespace, tmdNamespace, ttNamespace, videoSourceToken)
}

func media2ServiceHandler() gin.HandlerFunc {
	const (
		getAudioOutputConfigurations        = "GetAudioOutputConfigurations"
		getAudioSources                     = "GetAudioSources"
		getVideoSources                     = "GetVideoSources"
		getVideoSourceConfigurations        = "GetVideoSourceConfigurations"
		getVideoEncoderInstances            = "GetVideoEncoderInstances"
		getVideoEncoderConfigurationOptions = "GetVideoEncoderConfigurationOptions"
		getVideoEncoderConfigurations       = "GetVideoEncoderConfigurations"
		getStreamUri                        = "GetStreamUri"
		getMetadataConfigurationOptions     = "GetMetadataConfigurationOptions"
		getMetadataConfigurations           = "GetMetadataConfigurations"
		getAudioEncoderConfigurations       = "GetAudioEncoderConfigurations"
		getAudioSourceConfigurations        = "GetAudioSourceConfigurations"
		getProfiles                         = "GetProfiles"
		getOSDOptions                       = "GetOSDOptions"
	)

	return func(c *gin.Context) {
		var envelope soapEnvelope
		if err := xml.NewDecoder(c.Request.Body).Decode(&envelope); err != nil {
			appLogger.Warn("failed to parse media2 request", "err", err)
			c.Status(http.StatusBadRequest)
			return
		}

		bodyContent := strings.TrimSpace(envelope.Body.Raw)
		switch {
		case strings.Contains(bodyContent, getAudioOutputConfigurations):
			payload := buildMedia2GetAudioOutputConfigurationsResponse()
			c.Data(http.StatusOK, soapContentType, []byte(payload))
		case strings.Contains(bodyContent, getAudioSourceConfigurations):
			payload := buildMedia2GetAudioSourceConfigurationsResponse()
			c.Data(http.StatusOK, soapContentType, []byte(payload))
		case strings.Contains(bodyContent, getAudioEncoderConfigurations):
			payload := buildMedia2GetAudioEncoderConfigurationsResponse()
			c.Data(http.StatusOK, soapContentType, []byte(payload))
		case strings.Contains(bodyContent, getMetadataConfigurations):
			payload := buildMedia2GetMetadataConfigurationsResponse()
			c.Data(http.StatusOK, soapContentType, []byte(payload))
		case strings.Contains(bodyContent, getMetadataConfigurationOptions):
			payload := buildMedia2GetMetadataConfigurationOptionsResponse()
			c.Data(http.StatusOK, soapContentType, []byte(payload))
		case strings.Contains(bodyContent, getAudioSources):
			payload := buildMedia2GetAudioSourcesResponse()
			c.Data(http.StatusOK, soapContentType, []byte(payload))
		case strings.Contains(bodyContent, getVideoSources):
			payload := buildMedia2GetVideoSourcesResponse()
			c.Data(http.StatusOK, soapContentType, []byte(payload))
		case strings.Contains(bodyContent, getVideoSourceConfigurations):
			payload := buildMedia2GetVideoSourceConfigurationsResponse()
			c.Data(http.StatusOK, soapContentType, []byte(payload))
		case strings.Contains(bodyContent, getVideoEncoderConfigurationOptions):
			payload := buildMedia2GetVideoEncoderConfigurationOptionsResponse()
			c.Data(http.StatusOK, soapContentType, []byte(payload))
		case strings.Contains(bodyContent, getVideoEncoderConfigurations):
			payload := buildMedia2GetVideoEncoderConfigurationsResponse()
			c.Data(http.StatusOK, soapContentType, []byte(payload))
		case strings.Contains(bodyContent, getStreamUri):
			req, err := parseMedia2GetStreamUriRequest(bodyContent)
			if err != nil {
				appLogger.Warn("failed to parse GetStreamUri request", "err", err)
				c.Status(http.StatusBadRequest)
				return
			}

			if req.Protocol != RTSPProtocolRTSP {
				payload := buildMedia2InvalidStreamSetupFault(req.Protocol)
				c.Data(http.StatusInternalServerError, soapContentType, []byte(payload))
				return
			}

			payload := buildMedia2GetStreamUriResponse()
			c.Data(http.StatusOK, soapContentType, []byte(payload))
		case strings.Contains(bodyContent, getVideoEncoderInstances):
			payload := buildMedia2GetVideoEncoderInstancesResponse()
			c.Data(http.StatusOK, soapContentType, []byte(payload))
		case strings.Contains(bodyContent, getProfiles):
			payload := buildMedia2GetProfilesResponse()
			c.Data(http.StatusOK, soapContentType, []byte(payload))
		case strings.Contains(bodyContent, getOSDOptions):
			payload := buildMedia2GetOSDOptionsResponse()
			c.Data(http.StatusOK, soapContentType, []byte(payload))
		default:
			action := detectSOAPAction(bodyContent)
			appLogger.Warn("media2 request action not recognized", "action", action, "body", bodyContent)
			payload := buildActionNotSupportedFault(action)
			c.Data(http.StatusBadRequest, soapContentType, []byte(payload))
		}
	}
}

func buildMedia2GetOSDOptionsResponse() string {
	body := `<tr2:GetOSDOptionsResponse>
</tr2:GetOSDOptionsResponse>`

	return wrapMedia2Response(body)
}

func buildMedia2GetAudioOutputConfigurationsResponse() string {
	body := `<tr2:GetAudioOutputConfigurationsResponse>
</tr2:GetAudioOutputConfigurationsResponse>`

	return wrapMedia2Response(body)
}

func buildMedia2GetAudioSourceConfigurationsResponse() string {
	body := `<tr2:GetAudioSourceConfigurationsResponse>
</tr2:GetAudioSourceConfigurationsResponse>`

	return wrapMedia2Response(body)
}

func buildMedia2GetAudioEncoderConfigurationsResponse() string {
	body := `<tr2:GetAudioEncoderConfigurationsResponse>
</tr2:GetAudioEncoderConfigurationsResponse>`

	return wrapMedia2Response(body)
}

func buildMedia2GetMetadataConfigurationsResponse() string {
	body := `<tr2:GetMetadataConfigurationsResponse>
</tr2:GetMetadataConfigurationsResponse>`

	return wrapMedia2Response(body)
}

func buildMedia2GetMetadataConfigurationOptionsResponse() string {
	body := `<tr2:GetMetadataConfigurationOptionsResponse>
</tr2:GetMetadataConfigurationOptionsResponse>`

	return wrapMedia2Response(body)
}

func buildMedia2GetProfilesResponse() string {
	body := fmt.Sprintf(`<tr2:GetProfilesResponse>
	<tr2:Profiles token="%s" fixed="true">
		<tt:Name>%s</tt:Name>
		<tr2:Configurations>
			<tr2:VideoSource token="%s" ViewMode="Original">
				<tt:Name>%s</tt:Name>
				<tt:UseCount>1</tt:UseCount>
				<tt:SourceToken>%s</tt:SourceToken>
				<tt:Bounds x="0" y="0" width="1920" height="1080" />
			</tr2:VideoSource>
			<tr2:VideoEncoder token="%s" GovLength="60" AnchorFrameDistance="1" Profile="Baseline">
				<tt:Name>%s</tt:Name>
				<tt:UseCount>1</tt:UseCount>
				<tt:Encoding>H265</tt:Encoding>
				<tt:Resolution>
					<tt:Width>1920</tt:Width>
					<tt:Height>1080</tt:Height>
				</tt:Resolution>
				<tt:RateControl ConstantBitRate="false">
					<tt:FrameRateLimit>30</tt:FrameRateLimit>
					<tt:BitrateLimit>4096</tt:BitrateLimit>
				</tt:RateControl>
				<tt:Quality>5</tt:Quality>
			</tr2:VideoEncoder>
		</tr2:Configurations>
	</tr2:Profiles>
</tr2:GetProfilesResponse>`,
		mediaProfileToken,
		mediaProfileToken,
		videoSourceConfigToken,
		videoSourceConfigToken,
		videoSourceToken,
		videoEncoderConfigToken,
		videoEncoderConfigToken,
	)

	return wrapMedia2Response(body)
}

func buildMedia2GetAudioSourcesResponse() string {
	body := `<tr2:GetAudioSourcesResponse>
</tr2:GetAudioSourcesResponse>`

	return wrapMedia2Response(body)
}

func buildMedia2GetVideoSourcesResponse() string {
	body := fmt.Sprintf(`<tr2:GetVideoSourcesResponse>
	<tr2:VideoSources token="%s">
		<tt:Framerate>30</tt:Framerate>
		<tt:Resolution>
			<tt:Width>1920</tt:Width>
			<tt:Height>1080</tt:Height>
		</tt:Resolution>
	</tr2:VideoSources>
</tr2:GetVideoSourcesResponse>`, videoSourceToken)

	return wrapMedia2Response(body)
}

func buildMedia2GetVideoSourceConfigurationsResponse() string {
	body := fmt.Sprintf(`<tr2:GetVideoSourceConfigurationsResponse>
	<tr2:Configurations token="%s" ViewMode="Original">
		<tt:Name>%s</tt:Name>
		<tt:UseCount>1</tt:UseCount>
		<tt:SourceToken>%s</tt:SourceToken>
		<tt:Bounds x="0" y="0" width="1920" height="1080" />
		<tt:Extension>
			<tt:Rotate>
				<tt:Mode>OFF</tt:Mode>
			</tt:Rotate>
		</tt:Extension>
	</tr2:Configurations>
</tr2:GetVideoSourceConfigurationsResponse>`,
		videoSourceConfigToken,
		videoSourceConfigToken,
		videoSourceToken,
	)

	return wrapMedia2Response(body)
}

func buildMedia2GetVideoEncoderConfigurationsResponse() string {
	body := fmt.Sprintf(`<tr2:GetVideoEncoderConfigurationsResponse>
	<tr2:Configurations token="%s" GovLength="60" AnchorFrameDistance="1" Profile="Baseline">
		<tt:Name>%s</tt:Name>
		<tt:UseCount>1</tt:UseCount>
		<tt:Encoding>H265</tt:Encoding>
		<tt:Resolution>
			<tt:Width>1920</tt:Width>
			<tt:Height>1080</tt:Height>
		</tt:Resolution>
		<tt:RateControl ConstantBitRate="false">
			<tt:FrameRateLimit>30</tt:FrameRateLimit>
			<tt:EncodingInterval>1</tt:EncodingInterval>
			<tt:BitrateLimit>4096</tt:BitrateLimit>
		</tt:RateControl>
		<tt:Quality>5</tt:Quality>
	</tr2:Configurations>
</tr2:GetVideoEncoderConfigurationsResponse>`,
		videoEncoderConfigToken,
		videoEncoderConfigToken,
	)

	return wrapMedia2Response(body)
}

func buildMedia2GetVideoEncoderConfigurationOptionsResponse() string {
	body := `<tr2:GetVideoEncoderConfigurationOptionsResponse>
	<tr2:Options ConstantBitRateSupported="true" ProfilesSupported="Main" FrameRatesSupported="30 25 20 15 10" GovLengthRange="30 120">
		<tt:Encoding>H265</tt:Encoding>
		<tt:QualityRange>
			<tt:Min>1</tt:Min>
			<tt:Max>10</tt:Max>
		</tt:QualityRange>
		<tt:ResolutionsAvailable>
			<tt:Width>1920</tt:Width>
			<tt:Height>1080</tt:Height>
		</tt:ResolutionsAvailable>
		<tt:BitrateRange>
			<tt:Min>256</tt:Min>
			<tt:Max>8192</tt:Max>
		</tt:BitrateRange>
	</tr2:Options>
</tr2:GetVideoEncoderConfigurationOptionsResponse>`

	return wrapMedia2Response(body)
}

func buildMedia2GetVideoEncoderInstancesResponse() string {
	body := `<tr2:GetVideoEncoderInstancesResponse>
	<tr2:Info>
		<tr2:Total>1</tr2:Total>
	</tr2:Info>
</tr2:GetVideoEncoderInstancesResponse>`

	return wrapMedia2Response(body)
}

func buildMedia2GetStreamUriResponse() string {
	body := fmt.Sprintf(`<tr2:GetStreamUriResponse>
	<tr2:Uri>%s</tr2:Uri>
</tr2:GetStreamUriResponse>`, escapeXML(rtspURL))

	return wrapMedia2Response(body)
}

func buildServiceNotSupportedFault(path string) string {
	reason := "Service is not supported"
	if strings.TrimSpace(path) != "" {
		reason = fmt.Sprintf("Service %s is not supported", path)
	}
	reason = escapeXML(reason)

	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="%s"
	xmlns:ter="%s">
	<s:Body>
		<s:Fault>
			<s:Code>
				<s:Value>s:Sender</s:Value>
				<s:Subcode>
					<s:Value>ter:InvalidArgVal</s:Value>
					<s:Subcode>
						<s:Value>ter:ServiceNotSupported</s:Value>
					</s:Subcode>
				</s:Subcode>
			</s:Code>
			<s:Reason>
				<s:Text xml:lang="en">%s</s:Text>
			</s:Reason>
		</s:Fault>
	</s:Body>
</s:Envelope>`, soapNamespace, terNamespace, reason)
}

func buildActionNotSupportedFault(action string) string {
	reason := "Action is not supported"
	if strings.TrimSpace(action) != "" {
		reason = fmt.Sprintf("%s is not supported", action)
	}
	reason = escapeXML(reason)

	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="%s"
	xmlns:ter="%s">
	<s:Body>
		<s:Fault>
			<s:Code>
				<s:Value>s:Sender</s:Value>
				<s:Subcode>
					<s:Value>ter:ActionNotSupported</s:Value>
				</s:Subcode>
			</s:Code>
			<s:Reason>
				<s:Text xml:lang="en">%s</s:Text>
			</s:Reason>
		</s:Fault>
	</s:Body>
</s:Envelope>`, soapNamespace, terNamespace, reason)
}

func buildMedia2InvalidStreamSetupFault(protocol RTSPProtocol) string {
	reason := fmt.Sprintf("Protocol %s not supported", protocol)
	reason = escapeXML(reason)

	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="%s"
	xmlns:ter="%s">
	<s:Body>
		<s:Fault>
			<s:Code>
				<s:Value>s:Sender</s:Value>
				<s:Subcode>
					<s:Value>ter:InvalidArgVal</s:Value>
					<s:Subcode>
						<s:Value>ter:InvalidStreamSetup</s:Value>
					</s:Subcode>
				</s:Subcode>
			</s:Code>
			<s:Reason>
				<s:Text xml:lang="en">%s</s:Text>
			</s:Reason>
		</s:Fault>
	</s:Body>
</s:Envelope>`, soapNamespace, terNamespace, reason)
}

func wrapMedia2Response(body string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="%s"
	xmlns:tr2="%s"
	xmlns:tt="%s">
	<s:Body>
%s
	</s:Body>
</s:Envelope>`, soapNamespace, tr2Namespace, ttNamespace, body)
}

func escapeXML(input string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(input))
	return b.String()
}

func detectSOAPAction(body string) string {
	dec := xml.NewDecoder(strings.NewReader(body))
	for {
		tok, err := dec.Token()
		if err != nil {
			if err != io.EOF {
				appLogger.Warn("failed to parse SOAP action", "err", err)
			}
			return ""
		}

		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local != "" {
				return t.Name.Local
			}
		}
	}
}
