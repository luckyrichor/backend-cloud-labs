#include "expression.hpp"
#include <array>
#include <chrono>
#include <iostream>

int main(){
 constexpr std::size_t size=100000, repeats=10;
 std::vector<rules::Variables> maps;std::vector<std::array<double,2>> rows;
 maps.reserve(size);rows.reserve(size);
 for(std::size_t i=0;i<size;++i){double stock=double(i%4),price=double(i%200);maps.push_back({{"stock",stock},{"price",price}});rows.push_back({stock,price});}
 auto rule=rules::Parser("stock>0 && price*0.8<=100").compile();auto bound=rule->bind_variables(std::vector<std::string>{"stock","price"});
 std::size_t expected=0;
 for(std::size_t i=0;i<size;++i){auto a=rule->evaluate(maps[i]);auto b=bound.evaluate(std::span<const double>(rows[i]));if(a!=b)throw std::runtime_error("mismatch");expected+=a!=0;}
 auto run=[&](int mode){std::size_t matches=0;
  for(std::size_t r=0;r<repeats;++r)for(std::size_t i=0;i<size;++i){double value;
   if(mode==0)value=rule->evaluate(maps[i]);
   else if(mode==1)value=bound.evaluate(std::span<const double>(rows[i]));
   else {std::array<double,2> converted={maps[i].at("stock"),maps[i].at("price")};value=bound.evaluate(std::span<const double>(converted));}
   matches+=value!=0;
  }return matches;};
 for(int mode=0;mode<3;++mode)if(run(mode)!=expected*repeats)throw std::runtime_error("warmup mismatch");
 std::cout<<"round,mode,documents,evaluations,ns_per_doc,checksum\n";
 std::array<const char*,3> names={"hash_ast","slot_ast","convert_then_slot"};
 for(int round=0;round<10;++round)for(int offset=0;offset<3;++offset){int mode=(round+offset)%3;
  auto start=std::chrono::steady_clock::now();auto checksum=run(mode);auto end=std::chrono::steady_clock::now();
  if(checksum!=expected*repeats)throw std::runtime_error("timing mismatch");
  double ns=std::chrono::duration<double,std::nano>(end-start).count()/double(size*repeats);
  std::cout<<round<<','<<names[mode]<<','<<size<<','<<size*repeats<<','<<ns<<','<<checksum<<'\n';
 }
}
