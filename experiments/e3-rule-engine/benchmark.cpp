#include "expression.hpp"
#include <chrono>
#include <iostream>
int main() {
  const std::string source="stock>0 && price*0.8<=100";
  auto compiled=rules::Parser(source).compile(); rules::Variables vars{{"stock",3},{"price",100}};
  std::cout<<"mode,repeat,iterations,ns_per_operation,checksum\n";
  for(int mode=0;mode<2;++mode) for(int repeat=0;repeat<10;++repeat) {
    double sum=0; auto start=std::chrono::steady_clock::now();
    for(int i=0;i<100000;++i) { vars["price"]=90+i%60;
      if(mode) sum+=rules::Parser(source).compile()->evaluate(vars); else sum+=compiled->evaluate(vars);
    }
    auto ns=std::chrono::duration_cast<std::chrono::nanoseconds>(std::chrono::steady_clock::now()-start).count();
    std::cout<<(mode?"parse_and_evaluate":"cached_ast")<<','<<repeat<<",100000,"<<ns/100000.0<<','<<sum<<'\n';
  }
}
