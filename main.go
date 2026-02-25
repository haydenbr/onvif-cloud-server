package main

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// https://992d-73-99-84-195.ngrok-free.app

const (
	localHTTPPort           = 8081
	gatewayHTTPPort         = 443
	rtspHost                = "0.tcp.ngrok.io"
	rtspPort                = 18770
	soapNamespace           = "http://www.w3.org/2003/05/soap-envelope"
	tdsNamespace            = "http://www.onvif.org/ver10/device/wsdl"
	tr2Namespace            = "http://www.onvif.org/ver20/media/wsdl"
	ttNamespace             = "http://www.onvif.org/ver10/schema"
	tmdNamespace            = "http://www.onvif.org/ver10/deviceIO/wsdl"
	trtNamespace            = "http://www.onvif.org/ver10/media/wsdl"
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
	// router.Use(requireAuth())
	router.GET("/", func(c *gin.Context) {
		c.String(http.StatusNotFound, "not found")
	})
	router.POST("/onvif/device_service", deviceServiceHandler())
	router.POST("/onvif/deviceio_service", deviceIOServiceHandler())
	router.POST("/onvif/media_service", mediaServiceHandler())
	router.POST("/onvif/media2_service", media2ServiceHandler())
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusNotFound, "not found")
	})

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

// requireAuth enforces Basic auth header presence to trigger ONVIF security challenges early.
func requireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		header := strings.TrimSpace(c.GetHeader("Authorization"))
		if !isPOCAuthAccepted(header) {
			nonce := strconv.FormatInt(time.Now().UnixNano(), 10)
			c.Writer.Header().Add("WWW-Authenticate", fmt.Sprintf("Digest realm=\"ONVIF\", qop=\"auth\", nonce=\"%s\", algorithm=MD5", nonce))
			c.Writer.Header().Add("WWW-Authenticate", "Basic realm=\"ONVIF\"")
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}

		appLogger.Info("auth accepted", "scheme", detectAuthScheme(header))

		c.Next()
	}
}

func isPOCAuthAccepted(header string) bool {
	header = strings.TrimSpace(header)
	if header == "" {
		return false
	}

	lowerHeader := strings.ToLower(header)
	if strings.HasPrefix(lowerHeader, "basic ") {
		return isPOCBasicAuthAccepted(strings.TrimSpace(header[len("Basic "):]))
	}

	if strings.HasPrefix(lowerHeader, "digest ") {
		return isPOCDigestAuthAccepted(strings.TrimSpace(header[len("Digest "):]))
	}

	return false
}

func isPOCBasicAuthAccepted(encodedCredentials string) bool {
	if encodedCredentials == "" {
		return false
	}

	decoded, err := base64.StdEncoding.DecodeString(encodedCredentials)
	if err != nil {
		return false
	}

	username, password, ok := strings.Cut(string(decoded), ":")
	if !ok {
		return false
	}

	return strings.TrimSpace(username) != "" && password != ""
}

func isPOCDigestAuthAccepted(params string) bool {
	lowerParams := strings.ToLower(params)
	return strings.Contains(lowerParams, "username=") &&
		strings.Contains(lowerParams, "uri=") &&
		strings.Contains(lowerParams, "nonce=") &&
		strings.Contains(lowerParams, "response=")
}

func detectAuthScheme(header string) string {
	lowerHeader := strings.ToLower(strings.TrimSpace(header))
	if strings.HasPrefix(lowerHeader, "basic ") {
		return "basic"
	}
	if strings.HasPrefix(lowerHeader, "digest ") {
		return "digest"
	}
	return "unknown"
}

type soapEnvelope struct {
	Body struct {
		Raw string `xml:",innerxml"`
	} `xml:"Body"`
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

func requestSpecifiesIncludeCapability(body string) bool {
	type getServicesRequest struct {
		IncludeCapability string `xml:"IncludeCapability"`
	}

	var req getServicesRequest
	if err := xml.Unmarshal([]byte(body), &req); err != nil {
		return false
	}

	return strings.EqualFold(strings.TrimSpace(req.IncludeCapability), "true")
}

func deviceServiceHandler() gin.HandlerFunc {
	const (
		getServicesAction              = "GetServices"
		getServiceCapabilitiesAction   = "GetServiceCapabilities"
		getNetworkInterfacesAction     = "GetNetworkInterfaces"
		getDeviceInfoAction            = "GetDeviceInformation"
		getSystemDateAndTimeAction     = "GetSystemDateAndTime"
		getNetworkProtocolsAction      = "GetNetworkProtocols"
		getNetworkDefaultGatewayAction = "GetNetworkDefaultGateway"
		getCapabilitiesAction          = "GetCapabilities"
		getUsersAction                 = "GetUsers"
		getScopesAction                = "GetScopes"
		setScopesAction                = "SetScopes"
		getHostnameAction              = "GetHostname"
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
			includeCapabilities := requestSpecifiesIncludeCapability(bodyContent)
			payload := buildGetServicesResponse(scheme, host, includeCapabilities)
			c.Data(http.StatusOK, soapContentType, []byte(payload))
		case strings.Contains(bodyContent, getServiceCapabilitiesAction):
			payload := buildGetServiceCapabilitiesResponse()
			c.Data(http.StatusOK, soapContentType, []byte(payload))
		case strings.Contains(bodyContent, getNetworkInterfacesAction):
			payload := buildGetNetworkInterfacesResponse()
			c.Data(http.StatusOK, soapContentType, []byte(payload))
		case strings.Contains(bodyContent, getDeviceInfoAction):
			payload := buildGetDeviceInformationResponse()
			c.Data(http.StatusOK, soapContentType, []byte(payload))
		case strings.Contains(bodyContent, getSystemDateAndTimeAction):
			payload := buildGetSystemDateAndTimeResponse()
			c.Data(http.StatusOK, soapContentType, []byte(payload))
		case strings.Contains(bodyContent, getNetworkProtocolsAction):
			payload := buildGetNetworkProtocolsResponse()
			c.Data(http.StatusOK, soapContentType, []byte(payload))
		case strings.Contains(bodyContent, getNetworkDefaultGatewayAction):
			payload := buildGetNetworkDefaultGatewayResponse()
			c.Data(http.StatusOK, soapContentType, []byte(payload))
		case strings.Contains(bodyContent, getCapabilitiesAction):
			payload := buildGetCapabilitiesResponse(scheme, host)
			c.Data(http.StatusOK, soapContentType, []byte(payload))
		case strings.Contains(bodyContent, getUsersAction):
			payload := buildGetUsersResponse()
			c.Data(http.StatusOK, soapContentType, []byte(payload))
		case strings.Contains(bodyContent, getScopesAction):
			payload := buildGetScopesResponse()
			c.Data(http.StatusOK, soapContentType, []byte(payload))
		case strings.Contains(bodyContent, getHostnameAction):
			payload := buildGetHostnameResponse()
			c.Data(http.StatusOK, soapContentType, []byte(payload))
		case strings.Contains(bodyContent, setScopesAction):
			payload := buildActionNotSupportedFault(setScopesAction)
			c.Data(http.StatusBadRequest, soapContentType, []byte(payload))
		default:
			action := detectSOAPAction(bodyContent)
			appLogger.Warn("device_service request action not recognized", "action", action, "body", bodyContent)
			payload := buildActionNotSupportedFault(action)
			c.Data(http.StatusBadRequest, soapContentType, []byte(payload))
		}
	}
}

func buildGetServicesResponse(scheme, host string, includeCapabilities bool) string {
	deviceAddress := fmt.Sprintf("%s://%s/onvif/device_service", scheme, host)
	mediaAddress := fmt.Sprintf("%s://%s/onvif/media_service", scheme, host)
	media2Address := fmt.Sprintf("%s://%s/onvif/media2_service", scheme, host)
	deviceIOAddress := fmt.Sprintf("%s://%s/onvif/deviceio_service", scheme, host)

	deviceCapabilitiesSection := ""
	media1CapabilitiesSection := ""
	media2CapabilitiesSection := ""
	deviceIOCapabilitiesSection := ""
	if includeCapabilities {
		deviceCapabilitiesSection = `
				<tds:Capabilities>
					<tds:Capabilities>
						<tds:Network IPFilter="false" ZeroConfiguration="false" IPVersion6="false" DynDNS="false" Dot11Configuration="false" Dot1XConfigurations="0" HostnameFromDHCP="false" NTP="0" DHCPv6="false" />
						<tds:Security TLS1.0="false" TLS1.1="false" TLS1.2="false" OnboardKeyGeneration="false" AccessPolicyConfig="false" DefaultAccessPolicy="false" Dot1X="false" RemoteUserHandling="false" X.509Token="false" SAMLToken="false" KerberosToken="false" UsernameToken="false" HttpDigest="true" RELToken="false" JsonWebToken="false" SupportedEAPMethods="" MaxUsers="1" MaxUserNameLength="0" MaxPasswordLength="0" SecurityPolicies="" MaxPasswordHistory="0" HashingAlgorithms="MD5,SHA-256" />
						<tds:System DiscoveryResolve="false" DiscoveryBye="false" RemoteDiscovery="true" SystemBackup="false" SystemLogging="false" FirmwareUpgrade="false" HttpFirmwareUpgrade="false" HttpSystemBackup="false" HttpSystemLogging="false" HttpSupportInformation="false" StorageConfiguration="false" MaxStorageConfigurations="0" StorageConfigurationRenewal="false" GeoLocationEntries="1" AutoGeo="" StorageTypesSupported="" DiscoveryNotSupported="true" NetworkConfigNotSupported="true" UserConfigNotSupported="true" Addons="" HardwareType="Camera" />
						<tds:Misc AuxiliaryCommands="" />
					</tds:Capabilities>
				</tds:Capabilities>`
		media1CapabilitiesSection = `
				<tds:Capabilities>
					<trt:Capabilities SnapshotUri="false" Rotation="false" VideoSourceMode="false" OSD="false">
						<trt:ProfileCapabilities MaximumNumberOfProfiles="1" />
						<trt:StreamingCapabilities RTPMulticast="false" RTP_TCP="true" RTP_RTSP_TCP="true" />
					</trt:Capabilities>
				</tds:Capabilities>`
		media2CapabilitiesSection = `
				<tds:Capabilities>
					<tr2:Capabilities SnapshotUri="false" Rotation="false" VideoSourceMode="false" OSD="false" TemporaryOSDText="false" Mask="false" SourceMask="false" WebRTC="0">
						<tr2:ProfileCapabilities MaximumNumberOfProfiles="1" ConfigurationsSupported="VideoSource VideoEncoder" />
						<tr2:StreamingCapabilities RTSPStreaming="true" SecureRTSPStreaming="true" RTPMulticast="false" RTP_RTSP_TCP="true" NonAggregateControl="false" RTSPWebSocketUri="" AutoStartMulticast="false" />
						<tr2:MediaSigningCapabilities MediaSigningSupported="false" />
						<tr2:AudioClipCapabilities MaxAudioClipLimit="0" MaxAudioClipSize="0" SupportedAudioClipFormat="" />
					</tr2:Capabilities>
				</tds:Capabilities>`
		deviceIOCapabilitiesSection = `
				<tds:Capabilities>
					<tmd:Capabilities VideoSources="1" VideoOutputs="0" AudioSources="0" AudioOutputs="0" RelayOutputs="0" DigitalInputs="0" SerialPorts="0"></tmd:Capabilities>
				</tds:Capabilities>`
	}

	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="%s"
	xmlns:tds="%s"
	xmlns:tt="%s"
	xmlns:trt="%s"
	xmlns:tr2="%s"
	xmlns:tmd="%s">
	<s:Body>
		<tds:GetServicesResponse>
			<tds:Service>
				<tds:Namespace>%s</tds:Namespace>
				<tds:XAddr>%s</tds:XAddr>
				<tds:Version>
					<tt:Major>25</tt:Major>
					<tt:Minor>06</tt:Minor>
				</tds:Version>
				%s
			</tds:Service>
			<tds:Service>
				<tds:Namespace>%s</tds:Namespace>
				<tds:XAddr>%s</tds:XAddr>
				<tds:Version>
					<tt:Major>25</tt:Major>
					<tt:Minor>06</tt:Minor>
				</tds:Version>
				%s
			</tds:Service>
			<tds:Service>
				<tds:Namespace>%s</tds:Namespace>
				<tds:XAddr>%s</tds:XAddr>
				<tds:Version>
					<tt:Major>25</tt:Major>
					<tt:Minor>06</tt:Minor>
				</tds:Version>
				%s
			</tds:Service>
			<tds:Service>
				<tds:Namespace>%s</tds:Namespace>
				<tds:XAddr>%s</tds:XAddr>
				<tds:Version>
					<tt:Major>25</tt:Major>
					<tt:Minor>06</tt:Minor>
				</tds:Version>
				%s
			</tds:Service>
		</tds:GetServicesResponse>
	</s:Body>
	</s:Envelope>`,
		soapNamespace,
		tdsNamespace,
		ttNamespace,
		trtNamespace,
		tr2Namespace,
		tmdNamespace,
		tdsNamespace,
		deviceAddress,
		deviceCapabilitiesSection,
		trtNamespace,
		mediaAddress,
		media1CapabilitiesSection,
		tr2Namespace,
		media2Address,
		media2CapabilitiesSection,
		tmdNamespace,
		deviceIOAddress,
		deviceIOCapabilitiesSection,
	)
}

func buildGetServiceCapabilitiesResponse() string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="%s"
	xmlns:tds="%s">
	<s:Body>
		<tds:GetServiceCapabilitiesResponse>
			<tds:Capabilities>
				<tds:Network IPFilter="false" ZeroConfiguration="false" IPVersion6="false" DynDNS="false" Dot11Configuration="false" Dot1XConfigurations="0" HostnameFromDHCP="false" NTP="0" DHCPv6="false" />
				<tds:Security TLS1.0="false" TLS1.1="false" TLS1.2="false" OnboardKeyGeneration="false" AccessPolicyConfig="false" DefaultAccessPolicy="false" Dot1X="false" RemoteUserHandling="false" X.509Token="false" SAMLToken="false" KerberosToken="false" UsernameToken="false" HttpDigest="true" RELToken="false" JsonWebToken="false" SupportedEAPMethods="" MaxUsers="1" MaxUserNameLength="0" MaxPasswordLength="0" SecurityPolicies="" MaxPasswordHistory="0" HashingAlgorithms="MD5,SHA-256" />
				<tds:System DiscoveryResolve="false" DiscoveryBye="false" RemoteDiscovery="true" SystemBackup="false" SystemLogging="false" FirmwareUpgrade="false" HttpFirmwareUpgrade="false" HttpSystemBackup="false" HttpSystemLogging="false" HttpSupportInformation="false" StorageConfiguration="false" MaxStorageConfigurations="0" StorageConfigurationRenewal="false" GeoLocationEntries="1" AutoGeo="" StorageTypesSupported="" DiscoveryNotSupported="true" NetworkConfigNotSupported="true" UserConfigNotSupported="true" Addons="" HardwareType="Camera" />
				<tds:Misc AuxiliaryCommands="" />
			</tds:Capabilities>
		</tds:GetServiceCapabilitiesResponse>
	</s:Body>
</s:Envelope>`, soapNamespace, tdsNamespace)
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
          <tt:Enabled>false</tt:Enabled>
          <tt:Config>
            <tt:Manual>
              <tt:Address>0.0.0.0</tt:Address>
              <tt:PrefixLength>24</tt:PrefixLength>
            </tt:Manual>
            <tt:DHCP>false</tt:DHCP>
          </tt:Config>
        </tt:IPv4>
				<tt:IPv6>
					<tt:Enabled>false</tt:Enabled>
					<tt:Config>
						<tt:AcceptRouterAdvert>false</tt:AcceptRouterAdvert>
						<tt:DHCP>Off</tt:DHCP>
						<tt:Manual>
							<tt:Address></tt:Address>
							<tt:PrefixLength>64</tt:PrefixLength>
						</tt:Manual>
					</tt:Config>
				</tt:IPv6>
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

func buildGetSystemDateAndTimeResponse() string {
	nowLocal := time.Now()
	nowUTC := nowLocal.UTC()
	_, offset := nowLocal.Zone()
	tz := formatUTCOffset(offset)

	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="%s"
	xmlns:tds="%s"
	xmlns:tt="%s">
	<s:Body>
		<tds:GetSystemDateAndTimeResponse>
			<tds:SystemDateAndTime>
				<tt:DateTimeType>Manual</tt:DateTimeType>
				<tt:DaylightSavings>false</tt:DaylightSavings>
				<tt:TimeZone>
					<tt:TZ>%s</tt:TZ>
				</tt:TimeZone>
				<tt:UTCDateTime>
					<tt:Time>
						<tt:Hour>%02d</tt:Hour>
						<tt:Minute>%02d</tt:Minute>
						<tt:Second>%02d</tt:Second>
					</tt:Time>
					<tt:Date>
						<tt:Year>%d</tt:Year>
						<tt:Month>%02d</tt:Month>
						<tt:Day>%02d</tt:Day>
					</tt:Date>
				</tt:UTCDateTime>
				<tt:LocalDateTime>
					<tt:Time>
						<tt:Hour>%02d</tt:Hour>
						<tt:Minute>%02d</tt:Minute>
						<tt:Second>%02d</tt:Second>
					</tt:Time>
					<tt:Date>
						<tt:Year>%d</tt:Year>
						<tt:Month>%02d</tt:Month>
						<tt:Day>%02d</tt:Day>
					</tt:Date>
				</tt:LocalDateTime>
			</tds:SystemDateAndTime>
		</tds:GetSystemDateAndTimeResponse>
	</s:Body>
</s:Envelope>`,
		soapNamespace,
		tdsNamespace,
		ttNamespace,
		tz,
		nowUTC.Hour(),
		nowUTC.Minute(),
		nowUTC.Second(),
		nowUTC.Year(),
		int(nowUTC.Month()),
		nowUTC.Day(),
		nowLocal.Hour(),
		nowLocal.Minute(),
		nowLocal.Second(),
		nowLocal.Year(),
		int(nowLocal.Month()),
		nowLocal.Day(),
	)
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

func buildGetScopesResponse() string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="%s"
	xmlns:tds="%s"
	xmlns:tt="%s">
	<s:Body>
		<tds:GetScopesResponse>
			<tds:Scopes>
				<tt:ScopeDef>Fixed</tt:ScopeDef>
				<tt:ScopeItem>onvif://www.onvif.org/name/CoolCamera</tt:ScopeItem>
			</tds:Scopes>
			<tds:Scopes>
				<tt:ScopeDef>Fixed</tt:ScopeDef>
				<tt:ScopeItem>onvif://www.onvif.org/location/Lab</tt:ScopeItem>
			</tds:Scopes>
			<tds:Scopes>
				<tt:ScopeDef>Fixed</tt:ScopeDef>
				<tt:ScopeItem>onvif://www.onvif.org/hardware/CoolCamera</tt:ScopeItem>
			</tds:Scopes>
			<tds:Scopes>
				<tt:ScopeDef>Fixed</tt:ScopeDef>
				<tt:ScopeItem>onvif://www.onvif.org/Profile/T</tt:ScopeItem>
			</tds:Scopes>
			<tds:Scopes>
				<tt:ScopeDef>Fixed</tt:ScopeDef>
				<tt:ScopeItem>onvif://www.onvif.org/Profile/Streaming</tt:ScopeItem>
			</tds:Scopes>
			<tds:Scopes>
				<tt:ScopeDef>Fixed</tt:ScopeDef>
				<tt:ScopeItem>onvif://www.onvif.org/type/video_encoder</tt:ScopeItem>
			</tds:Scopes>
			<tds:Scopes>
				<tt:ScopeDef>Fixed</tt:ScopeDef>
				<tt:ScopeItem>onvif://www.onvif.org/VideoSourceNumber/1</tt:ScopeItem>
			</tds:Scopes>
		</tds:GetScopesResponse>
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

func buildGetHostnameResponse() string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="%s"
	xmlns:tds="%s"
	xmlns:tt="%s">
	<s:Body>
		<tds:GetHostnameResponse>
			<tds:HostnameInformation>
				<tt:FromDHCP>false</tt:FromDHCP>
				<tt:Name>my-camera</tt:Name>
			</tds:HostnameInformation>
		</tds:GetHostnameResponse>
	</s:Body>
</s:Envelope>`, soapNamespace, tdsNamespace, ttNamespace)
}

func buildGetNetworkDefaultGatewayResponse() string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="%s"
	xmlns:tds="%s"
	xmlns:tt="%s">
	<s:Body>
		<tds:GetNetworkDefaultGatewayResponse>
		</tds:GetNetworkDefaultGatewayResponse>
	</s:Body>
</s:Envelope>`, soapNamespace, tdsNamespace, ttNamespace)
}

func buildGetCapabilitiesResponse(scheme, host string) string {
	deviceAddress := fmt.Sprintf("%s://%s/onvif/device_service", scheme, host)
	mediaAddress := fmt.Sprintf("%s://%s/onvif/media_service", scheme, host)
	deviceIOAddress := fmt.Sprintf("%s://%s/onvif/deviceio_service", scheme, host)

	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="%s"
	xmlns:tds="%s"
	xmlns:tt="%s">
	<s:Body>
		<tds:GetCapabilitiesResponse>
			<tds:Capabilities>
				<tt:Device>
					<tt:XAddr>%s</tt:XAddr>
					<tt:Network>
						<tt:IPFilter>false</tt:IPFilter>
						<tt:ZeroConfiguration>false</tt:ZeroConfiguration>
						<tt:IPVersion6>false</tt:IPVersion6>
						<tt:DynDNS>false</tt:DynDNS>
					</tt:Network>
					<tt:System>
						<tt:DiscoveryResolve>false</tt:DiscoveryResolve>
						<tt:DiscoveryBye>false</tt:DiscoveryBye>
						<tt:RemoteDiscovery>false</tt:RemoteDiscovery>
						<tt:SystemBackup>false</tt:SystemBackup>
						<tt:SystemLogging>false</tt:SystemLogging>
						<tt:FirmwareUpgrade>false</tt:FirmwareUpgrade>
						<tt:SupportedVersions>
							<tt:Major>25</tt:Major>
							<tt:Minor>06</tt:Minor>
						</tt:SupportedVersions>
						<tt:Extension>
							<tt:HttpFirmwareUpgrade>false</tt:HttpFirmwareUpgrade>
							<tt:HttpSystemBackup>false</tt:HttpSystemBackup>
							<tt:HttpSystemLogging>false</tt:HttpSystemLogging>
							<tt:HttpSupportInformation>false</tt:HttpSupportInformation>
						</tt:Extension>
					</tt:System>
					<tt:IO>
						<tt:InputConnectors>0</tt:InputConnectors>
						<tt:RelayOutputs>0</tt:RelayOutputs>
					</tt:IO>
					<tt:Security>
						<tt:TLS1.1>true</tt:TLS1.1>
						<tt:TLS1.2>true</tt:TLS1.2>
						<tt:OnboardKeyGeneration>false</tt:OnboardKeyGeneration>
						<tt:AccessPolicyConfig>false</tt:AccessPolicyConfig>
						<tt:X.509Token>false</tt:X.509Token>
						<tt:SAMLToken>false</tt:SAMLToken>
						<tt:KerberosToken>false</tt:KerberosToken>
						<tt:RELToken>false</tt:RELToken>
					</tt:Security>
				</tt:Device>
				<tt:Media>
					<tt:XAddr>%s</tt:XAddr>
					<tt:StreamingCapabilities>
						<tt:RTPMulticast>false</tt:RTPMulticast>
						<tt:RTP_TCP>true</tt:RTP_TCP>
						<tt:RTP_RTSP_TCP>true</tt:RTP_RTSP_TCP>
					</tt:StreamingCapabilities>
					<tt:Extension>
						<tt:ProfileCapabilities>
							<tt:MaximumNumberOfProfiles>1</tt:MaximumNumberOfProfiles>
						</tt:ProfileCapabilities>
					</tt:Extension>
				</tt:Media>
				<tt:Extension>
					<tt:DeviceIO>
						<tt:XAddr>%s</tt:XAddr>
						<tt:VideoSources>1</tt:VideoSources>
						<tt:VideoOutputs>0</tt:VideoOutputs>
						<tt:AudioSources>0</tt:AudioSources>
						<tt:AudioOutputs>0</tt:AudioOutputs>
						<tt:RelayOutputs>0</tt:RelayOutputs>
					</tt:DeviceIO>
				</tt:Extension>
			</tds:Capabilities>
		</tds:GetCapabilitiesResponse>
	</s:Body>
</s:Envelope>`,
		soapNamespace,
		tdsNamespace,
		ttNamespace,
		deviceAddress,
		mediaAddress,
		deviceIOAddress,
	)
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
	const (
		getVideoSourcesAction = "GetVideoSources"
		getAudioSourcesAction = "GetAudioSources"
		getAudioOutputsAction = "GetAudioOutputs"
	)

	return func(c *gin.Context) {
		var envelope soapEnvelope
		if err := xml.NewDecoder(c.Request.Body).Decode(&envelope); err != nil {
			appLogger.Warn("failed to parse deviceio request", "err", err)
			c.Status(http.StatusBadRequest)
			return
		}

		bodyContent := strings.TrimSpace(envelope.Body.Raw)
		switch {
		case strings.Contains(bodyContent, getAudioSourcesAction):
			payload := buildDeviceIOGetAudioSourcesResponse()
			c.Data(http.StatusOK, soapContentType, []byte(payload))
		case strings.Contains(bodyContent, getAudioOutputsAction):
			payload := buildDeviceIOGetAudioOutputsResponse()
			c.Data(http.StatusOK, soapContentType, []byte(payload))
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

func buildDeviceIOGetAudioSourcesResponse() string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="%s"
	xmlns:tmd="%s"
	xmlns:tt="%s">
	<s:Body>
		<tmd:GetAudioSourcesResponse>
		</tmd:GetAudioSourcesResponse>
	</s:Body>
</s:Envelope>`, soapNamespace, tmdNamespace, ttNamespace)
}

func buildDeviceIOGetAudioOutputsResponse() string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="%s"
	xmlns:tmd="%s"
	xmlns:tt="%s">
	<s:Body>
		<tmd:GetAudioOutputsResponse>
		</tmd:GetAudioOutputsResponse>
	</s:Body>
</s:Envelope>`, soapNamespace, tmdNamespace, ttNamespace)
}

func mediaServiceHandler() gin.HandlerFunc {
	const (
		getServiceCapabilitiesAction = "GetServiceCapabilities"
	)

	return func(c *gin.Context) {
		var envelope soapEnvelope
		if err := xml.NewDecoder(c.Request.Body).Decode(&envelope); err != nil {
			appLogger.Warn("failed to parse media request", "err", err)
			c.Status(http.StatusBadRequest)
			return
		}

		bodyContent := strings.TrimSpace(envelope.Body.Raw)
		switch {
		case strings.Contains(bodyContent, getServiceCapabilitiesAction):
			payload := buildMediaGetServiceCapabilitiesResponse()
			c.Data(http.StatusOK, soapContentType, []byte(payload))
		default:
			action := detectSOAPAction(bodyContent)
			appLogger.Warn("media request action not recognized", "action", action, "body", bodyContent)
			payload := buildActionNotSupportedFault(action)
			c.Data(http.StatusBadRequest, soapContentType, []byte(payload))
		}
	}
}

func buildMediaGetServiceCapabilitiesResponse() string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="%s"
	xmlns:trt="%s">
	<s:Body>
		<trt:GetServiceCapabilitiesResponse>
			<trt:Capabilities SnapshotUri="false" Rotation="false" VideoSourceMode="false" OSD="false">
				<trt:ProfileCapabilities MaximumNumberOfProfiles="1" />
				<trt:StreamingCapabilities RTPMulticast="false" RTP_TCP="true" RTP_RTSP_TCP="true" />
			</trt:Capabilities>
		</trt:GetServiceCapabilitiesResponse>
	</s:Body>
</s:Envelope>`, soapNamespace, trtNamespace)
}

func media2ServiceHandler() gin.HandlerFunc {
	const (
		getAudioOutputConfigurations        = "GetAudioOutputConfigurations"
		getAudioSources                     = "GetAudioSources"
		getAudioOutputs                     = "GetAudioOutputs"
		getVideoSources                     = "GetVideoSources"
		getVideoSourceConfigurations        = "GetVideoSourceConfigurations"
		getVideoSourceConfigurationOptions  = "GetVideoSourceConfigurationOptions"
		getVideoEncoderInstances            = "GetVideoEncoderInstances"
		getVideoEncoderConfigurationOptions = "GetVideoEncoderConfigurationOptions"
		getVideoEncoderConfigurations       = "GetVideoEncoderConfigurations"
		getStreamUri                        = "GetStreamUri"
		setSynchronizationPoint             = "SetSynchronizationPoint"
		getMetadataConfigurationOptions     = "GetMetadataConfigurationOptions"
		getMetadataConfigurations           = "GetMetadataConfigurations"
		getAnalyticsConfigurations          = "GetAnalyticsConfigurations"
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
		case strings.Contains(bodyContent, getAudioOutputs):
			payload := buildMedia2GetAudioOutputsResponse()
			c.Data(http.StatusOK, soapContentType, []byte(payload))
		case strings.Contains(bodyContent, getAudioSourceConfigurations):
			payload := buildMedia2GetAudioSourceConfigurationsResponse()
			c.Data(http.StatusOK, soapContentType, []byte(payload))
		case strings.Contains(bodyContent, getAudioEncoderConfigurations):
			payload := buildMedia2GetAudioEncoderConfigurationsResponse()
			c.Data(http.StatusOK, soapContentType, []byte(payload))
		case strings.Contains(bodyContent, getAnalyticsConfigurations):
			payload := buildMedia2GetAnalyticsConfigurationsResponse()
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
		case strings.Contains(bodyContent, getVideoSourceConfigurationOptions):
			payload := buildMedia2GetVideoSourceConfigurationOptionsResponse()
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

			// if req.Protocol != RTSPProtocolRTSP {
			// 	payload := buildMedia2InvalidStreamSetupFault(req.Protocol)
			// 	c.Data(http.StatusInternalServerError, soapContentType, []byte(payload))
			// 	return
			// }

			payload := buildMedia2GetStreamUriResponse(req.Protocol)
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
		case strings.Contains(bodyContent, setSynchronizationPoint):
			payload := buildMedia2SetSynchronizationPointResponse()
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

func buildMedia2SetSynchronizationPointResponse() string {
	body := `<tr2:SetSynchronizationPointResponse>
</tr2:SetSynchronizationPointResponse>`

	return wrapMedia2Response(body)
}

func buildMedia2GetAudioOutputConfigurationsResponse() string {
	body := `<tr2:GetAudioOutputConfigurationsResponse>
</tr2:GetAudioOutputConfigurationsResponse>`

	return wrapMedia2Response(body)
}

func buildMedia2GetAudioOutputsResponse() string {
	body := `<tr2:GetAudioOutputsResponse>
</tr2:GetAudioOutputsResponse>`

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

func buildMedia2GetAnalyticsConfigurationsResponse() string {
	body := `<tr2:GetAnalyticsConfigurationsResponse>
</tr2:GetAnalyticsConfigurationsResponse>`

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

func buildMedia2GetVideoSourceConfigurationOptionsResponse() string {
	body := fmt.Sprintf(`<tr2:GetVideoSourceConfigurationOptionsResponse>
	<tr2:Options token="%s" MaximumNumberOfProfiles="2">
		<tt:BoundsRange>
			<tt:XRange>
				<tt:Min>0</tt:Min>
				<tt:Max>0</tt:Max>
			</tt:XRange>
			<tt:YRange>
				<tt:Min>0</tt:Min>
				<tt:Max>0</tt:Max>
			</tt:YRange>
			<tt:WidthRange>
				<tt:Min>1920</tt:Min>
				<tt:Max>1920</tt:Max>
			</tt:WidthRange>
			<tt:HeightRange>
				<tt:Min>1080</tt:Min>
				<tt:Max>1080</tt:Max>
			</tt:HeightRange>
		</tt:BoundsRange>
		<tt:VideoSourceTokensAvailable>%s</tt:VideoSourceTokensAvailable>
		<tt:Extension>
			<tt:Rotate>
				<tt:Mode>OFF</tt:Mode>
			</tt:Rotate>
		</tt:Extension>
	</tr2:Options>
</tr2:GetVideoSourceConfigurationOptionsResponse>`,
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

func buildMedia2GetStreamUriResponse(proto RTSPProtocol) string {
	urll := ""
	if proto != RTSPProtocolOverHttp {
		urll = rtspURL
	}

	body := fmt.Sprintf(`<tr2:GetStreamUriResponse>
	<tr2:Uri>%s</tr2:Uri>
</tr2:GetStreamUriResponse>`, escapeXML(urll))

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

func formatUTCOffset(offsetSeconds int) string {
	sign := '+'
	if offsetSeconds < 0 {
		sign = '-'
		offsetSeconds = -offsetSeconds
	}
	hours := offsetSeconds / 3600
	minutes := (offsetSeconds % 3600) / 60
	return fmt.Sprintf("UTC%c%02d:%02d", sign, hours, minutes)
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
