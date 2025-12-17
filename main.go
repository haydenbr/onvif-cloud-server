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
	port            = 8081
	soapNamespace   = "http://www.w3.org/2003/05/soap-envelope"
	tdsNamespace    = "http://www.onvif.org/ver10/device/wsdl"
	tr2Namespace    = "http://www.onvif.org/ver20/media/wsdl"
	ttNamespace     = "http://www.onvif.org/ver10/schema"
	soapContentType = "application/soap+xml; charset=utf-8"
)

var appLogger = slog.New(slog.NewTextHandler(os.Stdout, nil))

func main() {
	gin.SetMode(gin.ReleaseMode)

	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(requestLogger())
	router.POST("/onvif/device_service", deviceServiceHandler())
	// router.POST("/onvif/media2_service", media2ServiceHandler())

	router.NoRoute(func(c *gin.Context) {
		appLogger.Warn("NoRoute hit", "method", c.Request.Method, "path", c.Request.URL.Path)
		c.Status(http.StatusInternalServerError)
	})

	addr := ":" + strconv.Itoa(port)
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

func deviceServiceHandler() gin.HandlerFunc {
	const (
		getServicesAction          = "GetServices"
		getCapabilitiesAction      = "GetCapabilities"
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
			host = "localhost:" + strconv.Itoa(port)
		}

		bodyContent := strings.TrimSpace(envelope.Body.Raw)
		switch {
		case strings.Contains(bodyContent, getServicesAction):
			payload := buildGetServicesResponse(scheme, host)
			c.Data(http.StatusOK, soapContentType, []byte(payload))
		case strings.Contains(bodyContent, getCapabilitiesAction):
			payload := buildGetCapabilitiesResponse(scheme, host)
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
			appLogger.Warn("device_service request action not recognized", "body", bodyContent)
			c.Status(http.StatusBadRequest)
		}
	}
}

func buildGetServicesResponse(scheme, host string) string {
	deviceAddress := fmt.Sprintf("%s://%s/onvif/device_service", scheme, host)
	media2Address := fmt.Sprintf("%s://%s/onvif/media2_service", scheme, host)

	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="%s">
	<s:Body>
		<tds:GetServicesResponse xmlns:tds="%s" xmlns:tt="%s">
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
						<tds:System DiscoveryResolve="false" DiscoveryBye="false" RemoteDiscovery="false" SystemBackup="false" SystemLogging="false" FirmwareUpgrade="false" HttpFirmwareUpgrade="false" HttpSystemBackup="false" HttpSystemLogging="false" HttpSupportInformation="false" StorageConfiguration="false" MaxStorageConfigurations="0" StorageConfigurationRenewal="false" GeoLocationEntries="1" AutoGeo="" StorageTypesSupported="" DiscoveryNotSupported="true" NetworkConfigNotSupported="true" UserConfigNotSupported="true" Addons="" HardwareType="Camera" />
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
					<tr2:Capabilities xmlns:tr2="%s" SnapshotUri="false" Rotation="false" VideoSourceMode="false" OSD="false" TemporaryOSDText="false" Mask="false" SourceMask="false" WebRTC="0">
						<tr2:ProfileCapabilities MaximumNumberOfProfiles="1" ConfigurationsSupported="VideoSource,VideoEncoder" />
						<tr2:StreamingCapabilities RTSPStreaming="true" RTPMulticast="false" RTP_RTSP_TCP="true" NonAggregateControl="false" RTSPWebSocketUri="" AutoStartMulticast="false" SecureRTSPStreaming="true" />
						<tr2:MediaSigningCapabilities MediaSigningSupported="false" />
						<tr2:AudioClipCapabilities MaxAudioClipLimit="0" MaxAudioClipSize="0" SupportedAudioClipFormat="" />
					</tr2:Capabilities>
				</tds:Capabilities>
			</tds:Service>
		</tds:GetServicesResponse>
	</s:Body>
</s:Envelope>`, soapNamespace, tdsNamespace, ttNamespace, tdsNamespace, deviceAddress, tr2Namespace, media2Address, tr2Namespace)
}

func buildGetCapabilitiesResponse(scheme, host string) string {
	deviceXAddr := fmt.Sprintf("%s://%s/onvif/device_service", scheme, host)
	mediaXAddr := fmt.Sprintf("%s://%s/onvif/media2_service", scheme, host)

	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="%s">
	<s:Body>
		<tds:GetCapabilitiesResponse xmlns:tds="%s" xmlns:tt="%s">
			<tds:Capabilities>
				<tt:Device>
					<tt:XAddr>%s</tt:XAddr>
				</tt:Device>
				<tt:Media>
					<tt:XAddr>%s</tt:XAddr>
					<tt:StreamingCapabilities>
						<tt:RTPMulticast>false</tt:RTPMulticast>
						<tt:RTP_TCP>true</tt:RTP_TCP>
						<tt:RTP_RTSP_TCP>true</tt:RTP_RTSP_TCP>
					</tt:StreamingCapabilities>
				</tt:Media>
			</tds:Capabilities>
		</tds:GetCapabilitiesResponse>
	</s:Body>
</s:Envelope>`, soapNamespace, tdsNamespace, ttNamespace, deviceXAddr, mediaXAddr)
}

func buildGetNetworkInterfacesResponse() string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="%s">
	<s:Body>
		<tds:GetNetworkInterfacesResponse xmlns:tds="%s" xmlns:tt="%s">
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
              <tt:Address>192.168.0.100</tt:Address>
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
<s:Envelope xmlns:s="%s">
	<s:Body>
		<tds:GetDeviceInformationResponse xmlns:tds="%s" xmlns:tt="%s">
			<tds:Manufacturer>Flock Safety</tds:Manufacturer>
			<tds:Model>Condor</tds:Model>
			<tds:FirmwareVersion>v1.0</tds:FirmwareVersion>
			<tds:SerialNumber>serialnumber123</tds:SerialNumber>
			<tds:HardwareId>hardwareid123</tds:HardwareId>
		</tds:GetDeviceInformationResponse>
	</s:Body>
</s:Envelope>`, soapNamespace, tdsNamespace, ttNamespace)
}

func buildGetUsersResponse() string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="%s">
	<s:Body>
		<tds:GetUsersResponse xmlns:tds="%s" xmlns:tt="%s">
			<tds:User>
				<tt:Username>flock</tt:Username>
				<tt:UserLevel>User</tt:UserLevel>
			</tds:User>
		</tds:GetUsersResponse>
	</s:Body>
</s:Envelope>`, soapNamespace, tdsNamespace, ttNamespace)
}

func buildGetNetworkProtocolsResponse() string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="%s">
	<s:Body>
		<tds:GetNetworkProtocolsResponse xmlns:tds="%s" xmlns:tt="%s">
			<tds:NetworkProtocols>
				<tt:Name>HTTPS</tt:Name>
				<tt:Enabled>true</tt:Enabled>
				<tt:Port>%d</tt:Port>
			</tds:NetworkProtocols>
			<tds:NetworkProtocols>
				<tt:Name>RTSP</tt:Name>
				<tt:Enabled>true</tt:Enabled>
				<tt:Port>554</tt:Port>
			</tds:NetworkProtocols>
		</tds:GetNetworkProtocolsResponse>
	</s:Body>
</s:Envelope>`, soapNamespace, tdsNamespace, ttNamespace, port)
}

func buildGetAudioOutputConfigurationsResponse() string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="%s">
	<s:Body>
		<tr2:GetAudioOutputConfigurationsResponse xmlns:tr2="%s" xmlns:tt="%s">
			<tr2:Configurations/>
		</tr2:GetAudioOutputConfigurationsResponse>
	</s:Body>
</s:Envelope>`, soapNamespace, tr2Namespace, ttNamespace)
}

func buildGetAudioSourcesResponse() string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="%s">
	<s:Body>
		<tr2:GetAudioSourcesResponse xmlns:tr2="%s" xmlns:tt="%s">
			<tr2:AudioSources/>
		</tr2:GetAudioSourcesResponse>
	</s:Body>
</s:Envelope>`, soapNamespace, tr2Namespace, ttNamespace)
}

func buildGetVideoSourcesResponse() string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="%s">
	<s:Body>
		<tr2:GetVideoSourcesResponse xmlns:tr2="%s" xmlns:tt="%s">
			<tr2:VideoSources>
				<tt:VideoSource token="938c2c2e-e083-4494-8090-568373dc9e92">
					<tt:Framerate>20.0</tt:Framerate>
					<tt:Resolution>
						<tt:Width>1920</tt:Width>
						<tt:Height>1080</tt:Height>
					</tt:Resolution>
				</tt:VideoSource>
			</tr2:VideoSources>
		</tr2:GetVideoSourcesResponse>
	</s:Body>
</s:Envelope>`, soapNamespace, tr2Namespace, ttNamespace)
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

func media2ServiceHandler() gin.HandlerFunc {
	const (
		getAudioOutputConfigurationsAction = "GetAudioOutputConfigurations"
		getAudioSourcesAction              = "GetAudioSources"
		getVideoSourcesAction              = "GetVideoSources"
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
		case strings.Contains(bodyContent, getAudioOutputConfigurationsAction):
			payload := buildGetAudioOutputConfigurationsResponse()
			c.Data(http.StatusOK, soapContentType, []byte(payload))
		case strings.Contains(bodyContent, getAudioSourcesAction):
			payload := buildGetAudioSourcesResponse()
			c.Data(http.StatusOK, soapContentType, []byte(payload))
		case strings.Contains(bodyContent, getVideoSourcesAction):
			payload := buildGetVideoSourcesResponse()
			c.Data(http.StatusOK, soapContentType, []byte(payload))
		default:
			appLogger.Warn("media2 request action not recognized", "body", bodyContent)
			c.Status(http.StatusBadRequest)
		}
	}
}
