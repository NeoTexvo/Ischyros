# Overview
FabricSharp (hash)  project is a variant of Hyperledger Fabric 1.4, a permissioned blockchain platform from Hyperledger. 
Compared with the vanilla version, FabricSharp supports fine-grained secure data provenance, sharding, use of
trusted hardware (eg. SGX), and a blockchain native storage engine called ForkBase, to boost system performance.

Thanks to colleagues from [MediLOT](https://medilot.com), [NUS](https://www.comp.nus.edu.sg/~dbsystem/index.html), [SUTD](https://istd.sutd.edu.sg/people/faculty/dinh-tien-tuan-anh), [BIT](http://cs.bit.edu.cn/szdw/jsml/js/zmh/index.htm), [Zhejiang University](https://person.zju.edu.cn/0098112), [MZH Technologies](http://www.mzhtechnologies.com/) and other organizations for their contributions.

# Quick Start
* Build the chaincode environment
```
make ccenv
```
* Build the peer docker image
```
DOCKER_DYNAMIC_LINK=true make peer-docker
```
__NOTE__: FabricSharp relies on ForkBase[3] as the storage engine, which is close-sourced.
Hence FabricSharp can only be built and run within the docker container. Running `make peer` may fail. So far FabricSharp only touches on _peer_ process. Other executables remain intact and other cmds in Makefile should function the same as before. 

# Architecture
![architecture](architecture.png)

# Progress
The current master branch incorporates the optimization from [2] on the basis of Fabric v1.4.2. 
We dedicate another branch __vldb19__, which shows more details about [2], including the experimental baseline, scripts, chaincode examples and so on. 

We will soon merge the optimization in [1] to this master branch upon v1.4.2 and similarly dedicate another branch for [1]. 

# Old readme 
[old_readme](README.old.md)

# Papers. 
* [1] H. Dang, A. Dinh, D. Lohgin, E.-C. Chang, Q. Lin, B.C. Ooi: [Towards Scaling Blockchain Systems via Sharding](https://arxiv.org/pdf/1804.00399.pdf). ACM SIGMOD 2019
* [2] P. Ruan, G. Chen, A. Dinh, Q. Lin, B.C. Ooi, M. Zhang: [FineGrained, Secure and Efficient Data Provenance on Blockchain Systems](https://www.comp.nus.edu.sg/~ooibc/bcprovenance.pdf). VLDB 2019. 
* [3] S. Wang, T. T. A . Dinh, Q. Lin, Z. Xie, M. Zhang, Q. Cai, G. Chen, B.C. Ooi, P. Ruan: [ForkBase: An Efficient Storage Engine for Blockchain and Forkable Applications](https://www.comp.nus.edu.sg/~ooibc/forkbase.pdf). VLDB 2018. 
* [4] A. Dinh, R. Liu, M. Zhang, G. Chen, B.C. Ooi, J. Wang: [Untangling Blockchain: A Data Processing View of Blockchain Systems](https://ieeexplore.ieee.org/stamp/stamp.jsp?arnumber=8246573). IEEE Transactions on Knowledge and Data Engineering, July 2018. 
* [5] A. Dinh, J. Wang, G. Chen, R. Liu, B. C. Ooi, K.-L. Tan: [BLOCKBENCH: A Framework for Analysing Private Blockchains](https://www.comp.nus.edu.sg/~ooibc/blockbench.pdf). ACM SIGMOD 2017.
* [6] 
P. Ruan, G. Chen, A. Dinh, Q. Lin, D. Loghin, B. C. Ooi, M. Zhang: [Blockchains and Distributed Databases: a Twin Study](https://arxiv.org/pdf/1910.01310.pdf). 2019.
