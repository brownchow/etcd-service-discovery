package main

import (
	"go.etcd.io/etcd/clientv3"
	"time"
	"context"
	"go.etcd.io/etcd/mvcc/mvccpb"
	"sync"
	"log"
)

type ClientMon struct {
	client        *clientv3.Client
	serverList    map[string]string
	lock          sync.Mutex
}

// 初始化server端
func NewClientMon (addr []string)( *ClientMon, error){
	conf := clientv3.Config{
		Endpoints:   addr,
		DialTimeout: 5 * time.Second,
	}
	if client, err := clientv3.New(conf); err == nil {
		return &ClientMon{
			client:client,
			serverList:make(map[string]string),
		}, nil
	} else {
		return nil ,err
	}
}

// 获取prefix目录下所有内容，并返回
func (this * ClientMon) GetService(prefix string) ([]string ,error){
	resp, err := this.client.Get(context.Background(), prefix, clientv3.WithPrefix())
	if err != nil {
		return nil, err
	}
	
	// resp是从指定prefix服务下get回的value，extractAddrs将value内容存到list
	addrs := this.extractAddrs(resp)

    go this.watcher(prefix)
	return addrs ,nil
}

// 将获取到的prefix目录下所有内容存入list并返回
func (this *ClientMon) extractAddrs(resp *clientv3.GetResponse) []string {
	addrs := make([]string,0)
	if resp == nil || resp.Kvs == nil {
		return addrs
	}
	for i := range resp.Kvs {
		if v := resp.Kvs[i].Value; v != nil {
			this.SetServiceList(string(resp.Kvs[i].Key),string(resp.Kvs[i].Value))
			addrs = append(addrs, string(v))
		}
	}
	return addrs
}

// watch负责将监听到的put、delete请求存放到指定list
func (this *ClientMon) watcher(prefix string) {
	rch := this.client.Watch(context.Background(), prefix, clientv3.WithPrefix())
	for wresp := range rch {
		for _, ev := range wresp.Events {
			switch ev.Type {
			case mvccpb.PUT:
				this.SetServiceList(string(ev.Kv.Key),string(ev.Kv.Value))
			case mvccpb.DELETE:
				this.DelServiceList(string(ev.Kv.Key))
			}
		}
	}
}

func (this *ClientMon) SetServiceList(key,val string) {
	this.lock.Lock()
	defer this.lock.Unlock()
	this.serverList[key] = string(val)
	log.Println("set data key :",key,"val:",val)
}

func (this *ClientMon) DelServiceList(key string) {
	this.lock.Lock()
	defer this.lock.Unlock()
	delete(this.serverList,key)
	log.Println("del data key:", key)
}

func main () {
	client,_ := NewClientMon([]string{"127.0.0.1:2379"})
	client.GetService("/node")
}