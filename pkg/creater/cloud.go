package creater

// 零配置的默认云端存储。
//
// 目标：`storage-lock-cli --storage=cloud skills lock --id=x` 一条命令就能用，
// 不填 DSN、不注册账号、不装数据库。
//
// 工作机制：
//  1. 解析服务端地址（优先级：--dsn > STORAGE_LOCK_CLOUD_URL > 本地地址文件 > 内置默认）；
//  2. 首次使用时客户端自动向服务端领取一份租户 secret 并落盘；
//  3. secret 按**身份**落盘，不按地址 —— 所以服务端换域名后锁不会丢；
//  4. 本机所有 Agent 进程读同一份 secret，天然共享同一个命名空间；
//  5. 不同用户之间的 secret 不同，锁完全隔离。
//
// 关于"地址会变"这件事，见 go-remote-storage/client/endpoints.go 的完整说明。

import (
	"context"
	"fmt"
	"os"
	"strings"

	remote_client "github.com/storage-lock/go-remote-storage/client"
	"github.com/storage-lock/go-storage"
)

const (
	// DefaultCloudURLEnv 是覆盖默认云端地址的环境变量。
	DefaultCloudURLEnv = "STORAGE_LOCK_CLOUD_URL"

	// DefaultCloudURL 是内置的默认云端地址。
	//
	// 它只是"现在的最佳答案"，不是凭据的一部分：
	// 客户端凭据按身份命名，地址换了不会导致换租户。
	DefaultCloudURL = "https://locks.zhaixingren.cn"

	// CloudStorageName 是零配置云端后端在 --storage 里的名字。
	CloudStorageName = "cloud"
)

func init() {
	register(CloudStorageName, func(ctx context.Context, dsn string) (storage.Storage, error) {
		explicit := strings.TrimSpace(dsn)
		if explicit == "" {
			explicit = strings.TrimSpace(os.Getenv(DefaultCloudURLEnv))
		}
		base, fallbacks, identity := remote_client.ResolveCandidates(explicit, DefaultCloudURL)
		if base == "" {
			return nil, fmt.Errorf(
				"cloud storage needs a server URL: pass --dsn=<https://your-locks-server>, "+
					"set %s, or write %s",
				DefaultCloudURLEnv, mustEndpointsFile())
		}
		// TenantMode：不带 secret 时自动领取并落盘。
		// Secret 留空 —— 若本地已有缓存，客户端直接复用，不会重复领取。
		//
		// 地址全挂时再走签名的发现文档：内置默认地址是编译进二进制的常量，
		// 服务端迁机房后必然过时，不能让每个 Agent 自己改 --dsn 才能用。
		return remote_client.New(ctx, remote_client.Config{
			BaseURL: base,
			// 用户显式给了地址（--dsn 或 STORAGE_LOCK_CLOUD_URL）时不做地址发现：
			// 再去拉公网内置文档只会多等几秒，还可能把他悄悄带到别的服务上。
			ExplicitAddress:    explicit != "",
			Fallbacks:          fallbacks,
			TenantMode:         true,
			Identity:           identity,
			DiscoveryOnFailure: true,
		})
	})
}

func mustEndpointsFile() string {
	p, err := remote_client.EndpointsFile()
	if err != nil {
		return "~/.config/storage-lock/endpoints.json"
	}
	return p
}
