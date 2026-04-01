package main

import (
	"context"
	"fmt"
	"os"

	invokev1 "github.com/fireflycore/service-mesh/.gen/proto/acme/invoke/v1"
	"github.com/fireflycore/service-mesh/pkg/originalidentity"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	var target string
	var service string
	var namespace string
	var env string
	var payload string
	var originalSubject string

	cmd := &cobra.Command{
		Use:   "service-mesh-demo-client",
		Short: "Call the MVP sidecar and print the demo response",
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := grpc.DialContext(
				context.Background(),
				target,
				grpc.WithTransportCredentials(insecure.NewCredentials()),
			)
			if err != nil {
				return err
			}
			defer conn.Close()

			nestedReq, err := proto.Marshal(&invokev1.UnaryInvokeRequest{
				Method:  invokev1.MeshInvokeService_UnaryInvoke_FullMethodName,
				Payload: []byte(payload),
				Codec:   "raw",
			})
			if err != nil {
				return err
			}

			metadata := []*invokev1.MetadataEntry{}
			if originalSubject != "" {
				metadata = append(metadata, &invokev1.MetadataEntry{
					Key:    originalidentity.MetadataSubject,
					Values: []string{originalSubject},
				})
			}

			resp, err := invokev1.NewMeshInvokeServiceClient(conn).UnaryInvoke(context.Background(), &invokev1.UnaryInvokeRequest{
				Target: &invokev1.ServiceRef{
					Service:   service,
					Namespace: namespace,
					Env:       env,
				},
				Method: invokev1.MeshInvokeService_UnaryInvoke_FullMethodName,
				Context: &invokev1.InvocationContext{
					Metadata: metadata,
				},
				Payload: nestedReq,
				Codec:   "proto",
			})
			if err != nil {
				return err
			}

			var nestedResp invokev1.UnaryInvokeResponse
			if err := proto.Unmarshal(resp.GetPayload(), &nestedResp); err != nil {
				return err
			}

			fmt.Println(string(nestedResp.GetPayload()))
			return nil
		},
	}

	cmd.Flags().StringVar(&target, "target", "127.0.0.1:19091", "sidecar listen address")
	cmd.Flags().StringVar(&service, "service", "orders", "target service")
	cmd.Flags().StringVar(&namespace, "namespace", "default", "target namespace")
	cmd.Flags().StringVar(&env, "env", "dev", "target env")
	cmd.Flags().StringVar(&payload, "payload", "hello", "demo request payload")
	cmd.Flags().StringVar(&originalSubject, "original-subject", "", "optional original end-user subject")
	return cmd
}
