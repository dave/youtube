package main

import (
	"context"
	"log"
)

func main() {
	ctx := context.Background()

	service := &Service{}

	if err := service.Init(ctx); err != nil {
		log.Fatalf("Unable to initialise service: %v", err)
	}

	//if err := web(ctx); err != nil {
	//	log.Fatalf("Unable to starrt web server: %v", err)
	//}

}

//func web(ctx context.Context) error {
//	hostname, err := os.Hostname()
//	if err != nil {
//		return fmt.Errorf("get hostname: %w", err)
//	}
//
//	var ip string
//	var tlsConfig *tls.Config
//
//	if hostname == "Daves-MacBook-Pro.local" {
//		ip = "localhost"
//
//		cert, err := generateSelfSignedCert()
//		if err != nil {
//			return fmt.Errorf("failed to generate cert: %w", err)
//		}
//
//		tlsConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
//
//	} else {
//		ip = "uploader.wildernessprime.com"
//
//		certmagic.DefaultACME.Agreed = true
//		certmagic.DefaultACME.Email = "dave@brophy.uk"
//
//		m := certmagic.NewDefault()
//
//		if err := m.ManageSync(ctx, []string{ip}); err != nil {
//			return fmt.Errorf("manage sync: %w", err)
//		}
//		tlsConfig = m.TLSConfig()
//	}
//
//	server := http.Server{
//		Addr: ":443",
//		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
//			if r.URL.Path == "/" {
//				fmt.Fprintf(w, `<html><body>Sheet share link: <form action="/post" method="post"><input type="text" name="sheet" value="" /><input type="submit" value="send" /></form></body></html>`)
//			} else if r.URL.Path == "/post" {
//				//if err := r.ParseForm(); err != nil {
//				//	fmt.Fprintln(w, "error parsing form", err)
//				//}
//				//pwd := r.PostForm.Get("pwd")
//				//
//				//fmt.Fprintln(w, r.PostForm.Get("sheet"))
//			}
//		}),
//		TLSConfig: tlsConfig,
//	}
//
//	fmt.Println("Server running on https://" + ip)
//	err = server.ListenAndServeTLS("", "")
//	if err != nil {
//		return fmt.Errorf("ListenAndServeTLS: %w", err)
//	}
//	return nil
//}
//
//// Generate a self-signed TLS certificate in memory
//func generateSelfSignedCert() (tls.Certificate, error) {
//	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
//	if err != nil {
//		return tls.Certificate{}, err
//	}
//
//	template := x509.Certificate{
//		SerialNumber: big.NewInt(1),
//		Subject:      pkix.Name{CommonName: "localhost"},
//		NotBefore:    time.Now(),
//		NotAfter:     time.Now().Add(365 * 24 * time.Hour), // Valid for 1 year
//		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
//		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
//	}
//
//	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
//	if err != nil {
//		return tls.Certificate{}, err
//	}
//
//	privBytes, err := x509.MarshalECPrivateKey(priv)
//	if err != nil {
//		return tls.Certificate{}, err
//	}
//
//	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
//	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privBytes})
//
//	return tls.X509KeyPair(certPEM, keyPEM)
//}
