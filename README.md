# 用etcd实现分布式系统中的服务发现

infra 基础设施

## 背景

随着服务流量的增加，单台服务器已经不能满足我们的需求了，这时需要部署**很多台运行一样服务**的机器，这个就是最简单的分布式了



## 服务注册于发现的必要

最简单的场景，一般服务端架构是一台网关，网关后面是 n 台运行着一样服务的机器。客户端一般通过网关访问服务，然后网关把流量转到后面的服务器上。

考虑一个问题，集群中需要增加服务器的时候，最简单的办法是：加一台服务器，修改网关上的配置表，加上新的服务器 `ip` 和 端口，然后重启网关。还有就是，万一网关后面的集群中有服务器宕机，网关也是不知道的，还会把客户端请求转发到宕机的服务器上，造成服务不可用。



## etcd

解决上面的问题就要引入etcd了，当然也有很多其他方案，这里我们只介绍使用 etcd 的解决方案。

etcd是一个分布式的，可靠的 key-value 存储系统 https://etcd.io/

每次我们增加一个服务器，只需要向 etcd **注册该服务**（其实就是存一个key value）然后向etcd发送心跳，当 etcd 没有检测到这个心跳时会把这个 key-value 对删除（这个是 etcd 里的租约模式），网关只要关注所有的 key 就能知道所有的服务动态了



## 总结

本质上还是所有的服务所在的 ip 和 port 都写入到注册中心，然后需要的时候去注册中心查表 lookup table，如果注册中心挂了，这问题不太好解决，就好比机房断电了，你这咋拯救？



## 服务注册与发现的Go实现

### 服务注册

`serverReg.go`:

```go
package main

import (
	"context"
	"fmt"
	"time"

	"go.etcd.io/etcd/clientv3"
)

// 服务注册
type ServiceReg struct {
	client        *clientv3.Client
	lease         clientv3.Lease
	leaseResp     *clientv3.LeaseGrantResponse
	canclefunc    func()
	keepAliveChan <-chan *clientv3.LeaseKeepAliveResponse
	key           string
}

func NewServiceReg(addr []string, timeout int64) (*ServiceReg, error) {
	conf := clientv3.Config{Endpoints: addr, DialTimeout: timeout * time.Second,}
	var client *clientv3.Client

	if client, err := clientv3.New(conf)
	if err != nil{
		return nil, err
	}
	ser := &ServiceReg{client: client,}

	if err := ser.setLease(timeNum); err != nil {
		return nil, err
	}
	go ser.ListenLeaseRespChan()
	return ser, nil
}

//设置租约
func (this *ServiceReg) setLease(timeNum int64) error {
	lease := clientv3.NewLease(this.client)

	//设置租约时间
	leaseResp, err := lease.Grant(context.TODO(), timeNum)
	if err != nil {
		return err
	}

	//设置续租
	ctx, cancelFunc := context.WithCancel(context.TODO())
	leaseRespChan, err := lease.KeepAlive(ctx, leaseResp.ID)

	if err != nil {
		return err
	}

	this.lease = lease
	this.leaseResp = leaseResp
	this.canclefunc = cancelFunc
	this.keepAliveChan = leaseRespChan
	return nil
}

//监听 续租情况
func (this *ServiceReg) ListenLeaseRespChan() {
	for {
		select {
		case leaseKeepResp := <-this.keepAliveChan:
			if leaseKeepResp == nil {
				fmt.Printf("已经关闭续租功能\n")
				return
			} else {
				fmt.Printf("续租成功\n")
			}
		}
	}
}

//通过租约 注册服务
func (server *ServiceReg) PutService(key, val string) error {
	kv := clientv3.NewKV(server.client)
	_, err := kv.Put(context.TODO(), key, val, clientv3.WithLease(server.leaseResp.ID))
	return err
}

//撤销租约
func (server *ServiceReg) Revoke() error {
	server.canclefunc()
	time.Sleep(2 * time.Second)
	_, err := server.lease.Revoke(context.TODO(), server.leaseResp.ID)
	return err
}

func main() {
	ser, _ := NewServiceReg([]string{"127.0.0.1:2379"}, 5)
	ser.PutService("/node/111", "heiheihei")
	select {}
}
```

1、租约模式，客户端申请一个租约，并申请过期时间，每隔一段时间就自动续约。如果不续租，过期了，etcd 服务器会删除所这个租约上的所有 key-value

2、一般相同的服务存的key是一样的前缀，比如 

`server/0001   ===>  127.0.0.1:1212`  

`server/0002   ===>  127.0.0.1:1313`



### 服务发现

`serviceDisc.go`

```go
package main

import (
	"context"
	"log"
	"sync"
	"time"

	"go.etcd.io/etcd/clientv3"
	"go.etcd.io/etcd/mvcc/mvccpb"
)
// 服务发现
type ClientDis struct {
	client     *clientv3.Client
	serverList map[string]string
	lock       sync.Mutex
}

func NewClientDis(addr []string) (*ClientDis, error) {
	conf := clientv3.Config{
		Endpoints:   addr,
		DialTimeout: 5 * time.Second,
	}
	if client, err := clientv3.New(conf); err == nil {
		return &ClientDis{
			client:     client,
			serverList: make(map[string]string),
		}, nil
	} else {
		return nil, err
	}
}

func (this *ClientDis) GetService(prefix string) ([]string, error) {
	resp, err := this.client.Get(context.Background(), prefix, clientv3.WithPrefix())
	if err != nil {
		return nil, err
	}
	addrs := this.extractAddrs(resp)

	go this.watcher(prefix)
	return addrs, nil
}

func (this *ClientDis) watcher(prefix string) {
	rch := this.client.Watch(context.Background(), prefix, clientv3.WithPrefix())
	for wresp := range rch {
		for _, ev := range wresp.Events {
			switch ev.Type {
			case mvccpb.PUT:
				this.SetServiceList(string(ev.Kv.Key), string(ev.Kv.Value))
			case mvccpb.DELETE:
				this.DelServiceList(string(ev.Kv.Key))
			}
		}
	}
}

func (this *ClientDis) extractAddrs(resp *clientv3.GetResponse) []string {
	addrs := make([]string, 0)
	if resp == nil || resp.Kvs == nil {
		return addrs
	}
	for i := range resp.Kvs {
		if v := resp.Kvs[i].Value; v != nil {
			this.SetServiceList(string(resp.Kvs[i].Key), string(resp.Kvs[i].Value))
			addrs = append(addrs, string(v))
		}
	}
	return addrs
}

func (this *ClientDis) SetServiceList(key, val string) {
	this.lock.Lock()
	defer this.lock.Unlock()
	this.serverList[key] = string(val)
	log.Println("set data key :", key, "val:", val)
}

func (this *ClientDis) DelServiceList(key string) {
	this.lock.Lock()
	defer this.lock.Unlock()
	delete(this.serverList, key)
	log.Println("del data key:", key)
}

func (this *ClientDis) SerList2Array() []string {
	this.lock.Lock()
	defer this.lock.Unlock()
	addrs := make([]string, 0)

	for _, v := range this.serverList {
		addrs = append(addrs, v)
	}
	return addrs
}

func main() {
	cli, _ := NewClientDis([]string{"127.0.0.1:2379"})
	cli.GetService("/node")
	select {}
}
```

1、 Client 连接到 etcd

2、匹配到所有相同前缀的 key，存到 map 里

3、watch 这个key，有增加或删除的时候，就修改这个 map

4、map 就是当前集群中所有存活的服务的列表



## Refernece

https://www.jianshu.com/p/7c0d23c818a5







