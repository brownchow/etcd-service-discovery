package main

import (
    "context"
    "fmt"
    "go.etcd.io/etcd/clientv3"
    "time"
)

type Service struct {
    client *clientv3.Client
}

//Endpoints是etcd cluster的机器列表，DialTimeout是节点连接服务的超时时间
func NewService() *Service {
    config := clientv3.Config{
        Endpoints:   []string{"127.0.0.1:23791", "127.0.0.1:23792"},
        DialTimeout: 10 * time.Second,
    }
    client, _ := clientv3.New(config)
    return &Service{client: client}
}

//注册服务，id 唯一标识，name 服务名称，address 服务地址
func (this *Service) RegService(id string, name string, address string) error {
    kv := clientv3.NewKV(this.client)
    key_prefix := "/etcd_services/"
    ctx := context.Background()
    lease := clientv3.NewLease(this.client)
    
    //设置租约过期时间为20秒,跟命令行操作是一样的效果
    leaseRes, err := clientv3.NewLease(this.client).Grant(ctx, 20) 
    if err != nil {
        return err
    }
    _, err = kv.Put(context.Background(), key_prefix+id+"/"+name, address, clientv3.WithLease(leaseRes.ID)) //把服务的key绑定到租约下面
    if err != nil {
        return err
    }
	// 自动续租时间大概为租约时间的三分之一，context.TODO():你不知道要传什么，就传这个
	// lease.KeepAlive会定期执行？
    keepaliveRes, err := lease.KeepAlive(context.TODO(), leaseRes.ID)
    if err != nil {
        return err
	}
	// 起一个协程去读chan里的数据，否则会阻塞
    go lisKeepAlive(keepaliveRes)
    return err
}

func lisKeepAlive(keepaliveRes <-chan *clientv3.LeaseKeepAliveResponse) {
    for {
        select {
        case ret := <-keepaliveRes:
            if ret != nil {
                fmt.Println("续租成功", time.Now())
            }
        }
    }
}
