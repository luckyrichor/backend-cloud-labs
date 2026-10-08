#include "expression.hpp"
#include "string_baseline.hpp"
#include <algorithm>
#include <chrono>
#include <iostream>

int main() {
  const std::string source="stock>0 && price*0.8<=100";
  auto compiled=rules::Parser(source).compile();
  auto baseline=string_baseline::Parser(source).compile();
  std::cout<<"scenario,documents,mode,repeat,passes,ns_per_document,documents_per_second,matches,index_checksum\n";
  for (bool early : {false,true}) for (std::size_t size : {1000,10000,100000}) {
    std::vector<rules::Variables> docs; docs.reserve(size);
    for(std::size_t i=0;i<size;++i) docs.push_back({{"stock",early?double(i%4==0):3.0},{"price",double(90+i%60)}});
    const auto expected=compiled->filter_batch(docs);
    const int passes=static_cast<int>(1000000/size);
    // Warm every path, then rotate measurement order to reduce order bias.
    for(int repeat=-1;repeat<10;++repeat) for(int slot=0;slot<4;++slot) {
      int mode=(slot+std::max(repeat,0))%4;
      std::size_t count{},checksum{};
      auto start=std::chrono::steady_clock::now();
      for(int pass=0;pass<passes;++pass) {
        std::vector<std::size_t> found;
        if (mode==2) found=compiled->filter_batch(docs);
        else {
          found.reserve(size);
          for(std::size_t i=0;i<size;++i) {
            double result = mode==0 ? baseline->evaluate(docs[i]) :
              mode==1 ? compiled->evaluate(docs[i]) : rules::Parser(source).compile()->evaluate(docs[i]);
            if(result!=0) found.push_back(i);
          }
        }
        // Validate whole result, outside the measured interval (below).
        auto end=std::chrono::steady_clock::now();
        if(found!=expected) throw std::runtime_error("benchmark result mismatch");
        count+=found.size(); for(auto i:found) checksum+=i;
        start+=std::chrono::steady_clock::now()-end;
      }
      auto elapsed=std::chrono::duration<double,std::nano>(std::chrono::steady_clock::now()-start).count();
      const char* names[]={"string_scalar","enum_scalar","enum_batch","parse_enum_scalar"};
      double ns=elapsed/(size*passes);
      if(repeat>=0) std::cout<<(early?"early_short_circuit":"full_rhs")<<','<<size<<','<<names[mode]<<','<<repeat<<','<<passes<<','<<ns<<','<<1e9/ns<<','<<count<<','<<checksum<<'\n';
    }
  }
}
